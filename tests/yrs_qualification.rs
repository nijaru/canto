//! Native engine qualification, deliberately independent of Canto's application code.

use std::sync::Arc;
use yrs::undo::{Options as UndoOptions, UndoManager};
use yrs::updates::decoder::Decode;
use yrs::updates::encoder::{Encode, Encoder, EncoderV1};
use yrs::{
    Any, Assoc, ClientID, Doc, GetString, IndexedSequence, Map, OffsetKind, Options, Out, ReadTxn,
    Snapshot, StateVector, StickyIndex, Text, Transact, Update,
};

fn document(writer: u64) -> Doc {
    Doc::with_options(Options {
        client_id: ClientID::new(writer),
        skip_gc: true, // Yrs spelling of gc=false: retained deletions are needed for history.
        offset_kind: OffsetKind::Bytes,
        ..Options::default()
    })
}

fn content(doc: &Doc) -> String {
    doc.get_or_insert_text("text").get_string(&doc.transact())
}

fn insert(doc: &Doc, origin: &str, index: u32, value: &str) -> Vec<u8> {
    let text = doc.get_or_insert_text("text");
    let mut txn = doc.transact_mut_with(origin);
    text.insert(&mut txn, index, value);
    txn.encode_update_v1()
}

fn delete(doc: &Doc, origin: &str, index: u32, len: u32) -> Vec<u8> {
    let text = doc.get_or_insert_text("text");
    let mut txn = doc.transact_mut_with(origin);
    text.remove_range(&mut txn, index, len);
    txn.encode_update_v1()
}

fn apply(doc: &Doc, bytes: &[u8]) {
    let update = Update::decode_v1(bytes).expect("test fixture must decode");
    doc.transact_mut_with("remote")
        .apply_update(update)
        .expect("test fixture must integrate or remain causally pending");
}

fn full_state(doc: &Doc) -> Vec<u8> {
    doc.transact()
        .encode_state_as_update_v1(&StateVector::default())
}

fn replica(writer: u64, bytes: &[u8]) -> Doc {
    let doc = document(writer);
    apply(&doc, bytes);
    doc
}

fn sync(from: &Doc, to: &Doc) {
    let sv = to.transact().state_vector();
    apply(to, &from.transact().encode_state_as_update_v1(&sv));
}

fn undo_manager(doc: &Doc, origin: &str, timeout: u64) -> UndoManager<()> {
    let mut undo = UndoManager::with_options(UndoOptions {
        capture_timeout_millis: timeout,
        // A fixed nonzero clock makes grouping deterministic, without sleeps.
        timestamp: Arc::new(|| 1),
        ..UndoOptions::default()
    });
    undo.expand_scope(doc, &doc.get_or_insert_text("text"));
    undo.include_origin(origin);
    undo
}

fn anchor(doc: &Doc, index: u32) -> StickyIndex {
    doc.get_or_insert_text("text")
        .sticky_index(&doc.transact(), index, Assoc::After)
        .expect("fixture index must exist")
}

fn offsets(doc: &Doc, anchors: &[StickyIndex; 2]) -> [u32; 2] {
    let txn = doc.transact();
    anchors.each_ref().map(|a| {
        a.get_offset(&txn)
            .expect("retained fixture anchor must resolve")
            .index
    })
}

fn historical_state(doc: &Doc, pin: &Snapshot) -> Vec<u8> {
    let mut encoder = EncoderV1::new();
    doc.transact()
        .encode_state_from_snapshot(pin, &mut encoder)
        .expect("gc=false must retain the pinned history");
    encoder.to_vec()
}

#[test]
fn independent_insert_delete_converges_under_duplicate_and_reordered_updates() {
    let seed = document(9);
    insert(&seed, "seed", 0, "abcd");
    let base = full_state(&seed);
    let a = replica(1, &base);
    let b = replica(2, &base);
    assert_ne!(a.client_id(), b.client_id());

    // Both edit the same original text without observing the other's mutations.
    let updates = [
        insert(&a, "a", 1, "X"),
        delete(&a, "a", 3, 1), // delete original c, not X
        delete(&b, "b", 1, 1), // delete original b
        insert(&b, "b", 2, "Y"),
    ];
    assert_eq!(content(&a), "aXbd");
    assert_eq!(content(&b), "acYd");
    sync(&a, &b);
    sync(&b, &a);
    assert_eq!(content(&a), "aXYd");
    assert_eq!(content(&b), "aXYd");
    let expected = a.transact().snapshot();
    assert_eq!(b.transact().snapshot(), expected);

    // All 24 schedules, including delivery before a writer's prerequisite.
    for i in 0..4 {
        for j in (0..4).filter(|&j| j != i) {
            for k in (0..4).filter(|&k| k != i && k != j) {
                let l = (0..4).find(|&l| l != i && l != j && l != k).unwrap();
                let replay = replica(3, &base);
                for n in [i, j, k, l, l, k, j, i] {
                    apply(&replay, &updates[n]);
                }
                assert_eq!(content(&replay), "aXYd", "schedule {i},{j},{k},{l}");
                assert_eq!(replay.transact().snapshot(), expected);
                assert!(!replay.transact().has_missing_updates());
            }
        }
    }
}

#[test]
fn equal_bytes_are_not_equal_history_and_gc_false_pins_survive_reload() {
    let doc = document(1);
    insert(&doc, "local", 0, "x");
    let original = anchor(&doc, 0);
    let pin = doc.transact().snapshot();
    delete(&doc, "local", 0, 1);
    insert(&doc, "local", 0, "x");
    let replacement = anchor(&doc, 0);
    assert_eq!(content(&doc), "x");
    assert_ne!(original.id(), replacement.id());
    assert_ne!(pin, doc.transact().snapshot());

    let dir = tempfile::tempdir().unwrap();
    let state_path = dir.path().join("state.v1");
    let pin_path = dir.path().join("pin.v1");
    std::fs::write(&state_path, full_state(&doc)).unwrap();
    std::fs::write(&pin_path, pin.encode_v1()).unwrap();
    let reloaded = replica(2, &std::fs::read(state_path).unwrap());
    let pin = Snapshot::decode_v1(&std::fs::read(pin_path).unwrap()).unwrap();
    assert!(reloaded.skip_gc());
    assert_eq!(reloaded.transact().snapshot(), doc.transact().snapshot());
    assert_eq!(anchor(&reloaded, 0).id(), replacement.id());
    let historical = replica(3, &historical_state(&reloaded, &pin));
    assert_eq!(content(&historical), "x");
    assert_eq!(historical.transact().snapshot(), pin);
    assert_eq!(anchor(&historical, 0).id(), original.id());

    // A resumed replica gets a fresh writer; replay must not reauthor old items.
    insert(&reloaded, "local", 1, "!");
    sync(&reloaded, &doc);
    assert_eq!(content(&doc), "x!");
    assert_eq!(doc.transact().state_vector().get(&ClientID::new(2)), 1);
}

#[test]
fn full_state_reload_retains_pending_insertions_and_deletions() {
    for deletion in [false, true] {
        let source = document(1);
        let prerequisite = insert(&source, "local", 0, "x");
        let later = if deletion {
            delete(&source, "local", 0, 1)
        } else {
            insert(&source, "local", 1, "y")
        };
        let receiver = replica(2, &later);
        assert_eq!(content(&receiver), "");
        assert!(receiver.transact().has_missing_updates());
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("pending.v1");
        std::fs::write(&path, full_state(&receiver)).unwrap();
        let reloaded = replica(3, &std::fs::read(path).unwrap());
        assert!(reloaded.transact().has_missing_updates());
        apply(&reloaded, &prerequisite);
        assert!(!reloaded.transact().has_missing_updates());
        assert_eq!(content(&reloaded), if deletion { "" } else { "xy" });
        assert_eq!(reloaded.transact().snapshot(), source.transact().snapshot());
    }
}

#[test]
fn forks_and_selected_causally_complete_history_remain_isolated() {
    let seed = document(9);
    insert(&seed, "seed", 0, "base");
    let base = full_state(&seed);
    let task = replica(1, &base);
    let other = replica(2, &base);
    let selected = insert(&task, "task", 4, ":chosen");
    let unselected = insert(&task, "task", 11, ":later");
    insert(&other, "other", 4, ":other");

    let target = replica(3, &base);
    apply(&target, &selected);
    assert_eq!(content(&target), "base:chosen");
    assert_eq!(content(&seed), "base");
    assert_eq!(content(&task), "base:chosen:later");
    assert_eq!(content(&other), "base:other");
    assert!(
        !target
            .transact()
            .state_vector()
            .contains_client(&other.client_id())
    );
    let pinned_target = full_state(&target);
    insert(&task, "task", 17, ":future");
    let reloaded = replica(4, &pinned_target);
    assert_eq!(content(&reloaded), "base:chosen");
    assert_eq!(reloaded.transact().snapshot(), target.transact().snapshot());

    // Not arbitrary cherry-picking: the later same-writer edit requires its prefix.
    let incomplete = replica(5, &base);
    apply(&incomplete, &unselected);
    assert_eq!(content(&incomplete), "base");
    assert!(incomplete.transact().has_missing_updates());
    apply(&incomplete, &selected);
    assert_eq!(content(&incomplete), "base:chosen:later");
    assert!(!incomplete.transact().has_missing_updates());
    assert_eq!(content(&target), "base:chosen");
}

#[test]
fn origin_and_type_scoped_undo_redo_preserves_other_contributions() {
    let doc = document(1);
    let mut alpha = undo_manager(&doc, "alpha", 0);
    let mut beta = undo_manager(&doc, "beta", 0);
    insert(&doc, "alpha", 0, "A");
    insert(&doc, "beta", 1, "B");
    let peer = replica(2, &full_state(&doc));
    insert(&peer, "peer", 2, "C");
    sync(&peer, &doc);
    let metadata = doc.get_or_insert_map("metadata");
    metadata.insert(&mut doc.transact_mut_with("alpha"), "flag", true);
    assert_eq!(content(&doc), "ABC");
    assert_eq!(alpha.undo_stack().len(), 1);
    assert_eq!(beta.undo_stack().len(), 1);
    assert!(alpha.undo_blocking());
    assert_eq!(content(&doc), "BC");
    assert!(!alpha.can_undo());
    assert!(alpha.redo_blocking());
    assert_eq!(content(&doc), "ABC");
    assert!(beta.undo_blocking());
    assert_eq!(content(&doc), "AC");
    assert!(beta.redo_blocking());
    assert_eq!(content(&doc), "ABC");
    assert_eq!(
        metadata.get(&doc.transact(), "flag"),
        Some(Out::Any(Any::Bool(true)))
    );
    sync(&doc, &peer);
    assert_eq!(content(&peer), "ABC");
}

#[test]
fn grouped_x_to_y_undo_survives_remote_metadata_that_observed_x() {
    let a = document(1);
    let mut undo = undo_manager(&a, "local", 1_000);
    insert(&a, "local", 0, "x");
    let b = replica(2, &full_state(&a));
    assert_eq!(content(&b), "x");
    // B observed x before authoring metadata. The map has no text scope dependency.
    assert_eq!(b.transact().state_vector().get(&a.client_id()), 1);
    let metadata = b.get_or_insert_map("metadata");
    metadata.insert(&mut b.transact_mut_with("peer"), "flag", true);
    let text = a.get_or_insert_text("text");
    {
        let mut txn = a.transact_mut_with("local");
        text.remove_range(&mut txn, 0, 1);
        text.insert(&mut txn, 0, "y");
    }
    undo.reset();
    sync(&b, &a);
    assert_eq!(content(&a), "y");
    assert_eq!(undo.undo_stack().len(), 1);
    assert!(undo.undo_blocking());
    assert_eq!(content(&a), "");
    assert!(!undo.can_undo());
    assert_eq!(
        a.get_or_insert_map("metadata").get(&a.transact(), "flag"),
        Some(Out::Any(Any::Bool(true)))
    );
    sync(&a, &b);
    assert_eq!(content(&b), "");
    assert!(undo.redo_blocking());
    assert_eq!(content(&a), "y");
    sync(&a, &b);
    assert_eq!(content(&b), "y");
    assert_eq!(
        metadata.get(&b.transact(), "flag"),
        Some(Out::Any(Any::Bool(true)))
    );
}

#[test]
fn undo_local_insertion_preserves_interior_remote_insertion() {
    let a = document(1);
    let mut undo = undo_manager(&a, "local", 0);
    insert(&a, "local", 0, "abc");
    let b = replica(2, &full_state(&a));
    insert(&b, "peer", 1, "X");
    sync(&b, &a);
    assert_eq!(content(&a), "aXbc");
    assert!(undo.undo_blocking());
    assert_eq!(content(&a), "X");
    sync(&a, &b);
    assert_eq!(content(&b), "X");
    assert!(undo.redo_blocking());
    assert_eq!(content(&a), "aXbc");
    sync(&a, &b);
    assert_eq!(content(&b), "aXbc");
}

#[test]
fn double_deletion_undo_restores_fresh_identity_not_independent_delete_intent() {
    let seed = document(9);
    insert(&seed, "seed", 0, "abc");
    let a = replica(1, &full_state(&seed));
    let b = replica(2, &full_state(&seed));
    let original = anchor(&a, 1);
    let mut undo = undo_manager(&a, "local", 0);
    delete(&a, "local", 1, 1);
    delete(&b, "peer", 1, 1);
    sync(&a, &b);
    sync(&b, &a);
    assert_eq!(content(&a), "ac");
    assert_eq!(content(&b), "ac");
    assert!(undo.undo_blocking());
    // Delete sets target original IDs; undo inserts a new b, not a negative delete.
    assert_eq!(content(&a), "abc");
    let restored = anchor(&a, 1);
    assert_ne!(restored.id(), original.id());
    assert_eq!(restored.id().unwrap().client, a.client_id());
    let snapshot = a.transact().snapshot();
    assert!(snapshot.delete_set.contains(original.id().unwrap()));
    assert!(!snapshot.delete_set.contains(restored.id().unwrap()));
    assert!(snapshot.state_map.contains(restored.id().unwrap()));
    sync(&a, &b);
    assert_eq!(content(&b), "abc");
    assert_eq!(anchor(&b, 1).id(), restored.id());
    assert!(undo.redo_blocking());
    sync(&a, &b);
    assert_eq!(content(&a), "ac");
    assert_eq!(content(&b), "ac");
}

#[test]
fn ordinary_anchor_shifts_and_encoded_anchor_reload_preserve_positions() {
    let doc = document(1);
    insert(&doc, "local", 0, "A fox jumped");
    let anchors = [anchor(&doc, 2), anchor(&doc, 5)];
    insert(&doc, "local", 0, "!");
    assert_eq!(offsets(&doc, &anchors), [3, 6]);
    let encoded = anchors.each_ref().map(Encode::encode_v1);
    let reloaded = replica(2, &full_state(&doc));
    let decoded = encoded
        .each_ref()
        .map(|a| StickyIndex::decode_v1(a).unwrap());
    assert_eq!(decoded, anchors);
    assert_eq!(offsets(&reloaded, &decoded), [3, 6]);
    let unrelated = document(3);
    unrelated.get_or_insert_text("text");
    assert!(decoded[0].get_offset(&unrelated.transact()).is_none());
}

#[test]
fn deleted_range_pin_is_historical_but_undo_anchor_repair_is_not_replicated() {
    let a = document(1);
    insert(&a, "seed", 0, "A fox jumped");
    let b = replica(2, &full_state(&a));
    let anchors = [anchor(&a, 2), anchor(&a, 5)];
    let encoded = anchors.each_ref().map(Encode::encode_v1);
    let pin = a.transact().snapshot();
    let mut undo = undo_manager(&a, "local", 0);
    assert_eq!(offsets(&a, &anchors), [2, 5]);
    delete(&a, "local", 2, 3);
    sync(&a, &b);
    assert_eq!(offsets(&a, &anchors), [2, 2]);
    assert_eq!(offsets(&b, &anchors), [2, 2]);
    assert!(undo.undo_blocking());
    assert_eq!(content(&a), "A fox jumped");
    sync(&a, &b);
    let reloaded = replica(3, &full_state(&a));
    let decoded = encoded
        .each_ref()
        .map(|v| StickyIndex::decode_v1(v).unwrap());
    assert_eq!(offsets(&a, &decoded), [2, 5]);
    assert_eq!(offsets(&b, &decoded), [5, 5]);
    assert_eq!(offsets(&reloaded, &decoded), [5, 5]);
    assert_eq!(content(&b), content(&a));
    assert_eq!(content(&reloaded), content(&a));
    let pinned = replica(4, &historical_state(&reloaded, &pin));
    assert_eq!(content(&pinned), "A fox jumped");
    assert_eq!(offsets(&pinned, &decoded), [2, 5]);
}

#[test]
fn undo_manager_restart_does_not_restore_stacks_from_document_state() {
    let doc = document(1);
    let mut undo = undo_manager(&doc, "local", 0);
    insert(&doc, "local", 0, "x");
    insert(&doc, "local", 1, "y");
    assert!(undo.undo_blocking());
    assert_eq!(content(&doc), "x");
    assert!(undo.can_undo());
    assert!(undo.can_redo());
    let state = full_state(&doc);
    drop(undo);
    let mut replacement = undo_manager(&doc, "local", 0);
    assert!(!replacement.can_undo());
    assert!(!replacement.can_redo());
    assert!(!replacement.undo_blocking());
    let reloaded = replica(2, &state);
    let restarted = undo_manager(&reloaded, "local", 0);
    assert!(!restarted.can_undo());
    assert!(!restarted.can_redo());
    assert_eq!(content(&reloaded), "x");
}

#[test]
fn malformed_truncated_and_invalid_utf8_updates_return_decode_errors() {
    let source = document(1);
    let valid = insert(&source, "local", 0, "wire");
    for end in 0..valid.len() {
        assert!(
            Update::decode_v1(&valid[..end]).is_err(),
            "prefix length {end}"
        );
    }
    for invalid in [&[0xff][..], &[1, 0][..]] {
        assert!(Update::decode_v1(invalid).is_err());
    }
    let mut invalid_utf8 = valid.clone();
    let start = valid.windows(4).position(|v| v == b"wire").unwrap();
    invalid_utf8[start] = 0xff;
    assert!(Update::decode_v1(&invalid_utf8).is_err());
}

#[test]
fn update_decoder_accepts_trailing_bytes_so_transport_needs_its_own_framing() {
    let source = document(1);
    let mut bytes = insert(&source, "local", 0, "x");
    bytes.extend_from_slice(&[0xff, 0xff]);
    let destination = replica(2, &bytes);
    assert_eq!(content(&destination), "x");
    assert_eq!(
        destination.transact().snapshot(),
        source.transact().snapshot()
    );
}

#[test]
fn utf8_bytes_bom_nul_combining_marks_and_crlf_roundtrip_without_normalization() {
    let input = "\u{feff}fn café() {\r\n\tlet s = \"🦀e\u{301}\0\";\r\n}\r\n";
    let source = document(1);
    insert(&source, "local", 0, input);
    let text = source.get_or_insert_text("text");
    assert_eq!(
        text.len(&source.transact()),
        u32::try_from(input.len()).unwrap()
    );
    assert_eq!(content(&source).as_bytes(), input.as_bytes());
    let pin = source.transact().snapshot();
    let peer = replica(2, &full_state(&source));
    assert_eq!(content(&peer).as_bytes(), input.as_bytes());
    let at = u32::try_from(input.find('🦀').unwrap()).unwrap();
    delete(&peer, "peer", at, 4);
    insert(&peer, "peer", at, "雪");
    sync(&peer, &source);
    let expected = input.replace('🦀', "雪");
    assert_eq!(content(&source).as_bytes(), expected.as_bytes());
    let dir = tempfile::tempdir().unwrap();
    let path = dir.path().join("utf8.v1");
    std::fs::write(&path, full_state(&source)).unwrap();
    let reloaded = replica(3, &std::fs::read(path).unwrap());
    assert_eq!(content(&reloaded).as_bytes(), expected.as_bytes());
    let historical = replica(4, &historical_state(&reloaded, &pin));
    assert_eq!(content(&historical).as_bytes(), input.as_bytes());
}

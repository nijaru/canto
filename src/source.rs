use crate::{Error, Result};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use uuid::Uuid;
use yrs::{
    Doc, GetString, OffsetKind, Options, ReadTxn, StateVector, Text, Transact, Update,
    updates::decoder::Decode,
};

pub(crate) const FORMAT: &str = "canto-experimental-1/yrs-0.28.0-v1";
pub(crate) const MAX_FILE: usize = 16 * 1024 * 1024;
pub(crate) const MAX_RECORD: usize = 64 * 1024 * 1024;

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub enum FileKind {
    Regular,
    Symlink,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct File {
    pub document: String,
    pub kind: FileKind,
    pub executable: bool,
    pub bytes: Vec<u8>,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq, Eq)]
pub struct Manifest {
    pub files: BTreeMap<String, File>,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Edit {
    pub path: String,
    /// UTF-8 byte offsets, relative to the result of preceding edits in this batch.
    pub start: u32,
    pub end: u32,
    pub insert: String,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub(crate) struct Writer {
    pub epoch: String,
    pub client: u64,
    pub actor: String,
}

impl Writer {
    pub fn new(actor: &str) -> Self {
        Self {
            epoch: Uuid::new_v4().to_string(),
            client: yrs::ClientID::random().get(),
            actor: actor.into(),
        }
    }

    pub fn validate(&self) -> Result<()> {
        if self.client >= (1 << 53)
            || Uuid::parse_str(&self.epoch).is_err()
            || self.actor.is_empty()
            || self.actor.len() > 256
        {
            return Err(Error::Invalid("writer identity".into()));
        }
        Ok(())
    }
}

/// One immutable, independently authored causal batch. Never reuse its writer for another batch.
/// This is an experimental local interchange envelope, not a network protocol.
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CausalBatch {
    pub(crate) format: String,
    pub(crate) workspace: String,
    pub(crate) base: String,
    pub(crate) writer: Writer,
    pub(crate) edits: Vec<Edit>,
    pub(crate) updates: BTreeMap<String, Vec<u8>>,
}

impl CausalBatch {
    pub fn writer_epoch(&self) -> &str {
        &self.writer.epoch
    }
    pub fn base(&self) -> &str {
        &self.base
    }
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct NativeFile {
    pub file: File,
    pub text: Option<Vec<u8>>,
}

#[derive(Serialize, Deserialize)]
pub(crate) enum Body {
    Seed(BTreeMap<String, NativeFile>),
    Import {
        put: BTreeMap<String, NativeFile>,
        remove: Vec<String>,
    },
    Edit(CausalBatch),
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub(crate) struct Record {
    pub format: String,
    pub workspace: String,
    /// Inclusion order. The independently authored batch retains its own original base.
    pub parent: Option<String>,
    pub writer: Writer,
    pub body: Body,
}

struct Entry {
    file: File,
    doc: Option<Doc>,
}
#[derive(Default)]
pub(crate) struct State {
    files: BTreeMap<String, Entry>,
}

fn doc(client: u64) -> Doc {
    Doc::with_options(Options {
        client_id: yrs::ClientID::new(client),
        skip_gc: true,
        offset_kind: OffsetKind::Bytes,
        ..Options::default()
    })
}

fn import(doc: &Doc, bytes: &[u8]) -> Result<()> {
    let update =
        Update::decode_v1(bytes).map_err(|e| Error::Invalid(format!("Yrs update: {e}")))?;
    let mut txn = doc.transact_mut();
    txn.apply_update(update)
        .map_err(|e| Error::Invalid(format!("Yrs application: {e}")))?;
    if txn.store().pending_update().is_some() || txn.store().pending_ds().is_some() {
        return Err(Error::Invalid("unresolved causal dependencies".into()));
    }
    Ok(())
}

fn export(doc: &Doc) -> Vec<u8> {
    doc.transact()
        .encode_state_as_update_v1(&StateVector::default())
}
fn text_bytes(doc: &Doc) -> Vec<u8> {
    doc.get_or_insert_text("source")
        .get_string(&doc.transact())
        .into_bytes()
}
fn is_text(file: &File) -> bool {
    file.kind == FileKind::Regular && std::str::from_utf8(&file.bytes).is_ok()
}

pub(crate) fn validate_path(path: &str) -> Result<()> {
    if path.is_empty()
        || path.len() > 4096
        || path.contains(['\0', '\\'])
        || path.split('/').any(|p| {
            p.is_empty()
                || p == "."
                || p == ".."
                || p.eq_ignore_ascii_case(".git")
                || p.eq_ignore_ascii_case(".canto")
        })
    {
        return Err(Error::Unsupported(format!("path {path:?}")));
    }
    Ok(())
}

impl Entry {
    fn new(file: File, client: u64) -> Result<Self> {
        if file.bytes.len() > MAX_FILE {
            return Err(Error::Unsupported("file size exceeds 16 MiB".into()));
        }
        let doc = if is_text(&file) {
            let d = doc(client);
            let text =
                std::str::from_utf8(&file.bytes).map_err(|e| Error::Invalid(e.to_string()))?;
            d.get_or_insert_text("source")
                .insert(&mut d.transact_mut(), 0, text);
            Some(d)
        } else {
            None
        };
        Ok(Self { file, doc })
    }

    fn from_native(native: NativeFile) -> Result<Self> {
        if native.file.bytes.len() > MAX_FILE || native.text.is_some() != is_text(&native.file) {
            return Err(Error::Corrupt("invalid file encoding".into()));
        }
        let doc = if let Some(update) = native.text {
            let d = doc(0);
            import(&d, &update)?;
            if text_bytes(&d) != native.file.bytes {
                return Err(Error::Corrupt("text projection mismatch".into()));
            }
            Some(d)
        } else {
            None
        };
        Ok(Self {
            file: native.file,
            doc,
        })
    }

    fn native(&self) -> NativeFile {
        NativeFile {
            file: self.file.clone(),
            text: self.doc.as_ref().map(export),
        }
    }
}

impl State {
    pub fn seed(manifest: Manifest, writer: &Writer) -> Result<(Self, Body)> {
        let mut state = Self::default();
        for (path, file) in manifest.files {
            validate_path(&path)?;
            state.files.insert(path, Entry::new(file, writer.client)?);
        }
        let native = state
            .files
            .iter()
            .map(|(path, entry)| (path.clone(), entry.native()))
            .collect();
        Ok((state, Body::Seed(native)))
    }

    pub fn capture(&mut self, manifest: Manifest, writer: &Writer) -> Result<Body> {
        let remove: Vec<_> = self
            .files
            .keys()
            .filter(|p| !manifest.files.contains_key(*p))
            .cloned()
            .collect();
        for path in &remove {
            self.files.remove(path);
        }
        let mut put = BTreeMap::new();
        for (path, mut file) in manifest.files {
            validate_path(&path)?;
            if let Some(old) = self.files.get(&path) {
                file.document = old.file.document.clone();
                if file == old.file {
                    continue;
                }
                if is_text(&file) && old.doc.is_some() {
                    if file.bytes != old.file.bytes {
                        let before = std::str::from_utf8(&old.file.bytes)
                            .map_err(|e| Error::Corrupt(e.to_string()))?;
                        let after = std::str::from_utf8(&file.bytes)
                            .map_err(|e| Error::Invalid(e.to_string()))?;
                        // Observation supplies values, not an edit log. Preserve only
                        // the provably unchanged prefix/suffix; do not guess inner lineage.
                        let prefix: usize = before
                            .chars()
                            .zip(after.chars())
                            .take_while(|(a, b)| a == b)
                            .map(|(c, _)| c.len_utf8())
                            .sum();
                        let suffix: usize = before[prefix..]
                            .chars()
                            .rev()
                            .zip(after[prefix..].chars().rev())
                            .take_while(|(a, b)| a == b)
                            .map(|(c, _)| c.len_utf8())
                            .sum();
                        let replacement = Edit {
                            path: path.clone(),
                            start: u32::try_from(prefix)
                                .map_err(|_| Error::Invalid("text length".into()))?,
                            end: u32::try_from(before.len() - suffix)
                                .map_err(|_| Error::Invalid("text length".into()))?,
                            insert: after[prefix..after.len() - suffix].into(),
                        };
                        let updates = self.author_updates(&[replacement], writer)?;
                        self.apply_updates(&updates)?;
                    }
                    let entry = self
                        .files
                        .get_mut(&path)
                        .ok_or_else(|| Error::Missing(path.clone()))?;
                    entry.file = file;
                    put.insert(path, entry.native());
                    continue;
                }
            }
            let entry = Entry::new(file, writer.client)?;
            put.insert(path.clone(), entry.native());
            self.files.insert(path, entry);
        }
        Ok(Body::Import { put, remove })
    }

    pub fn manifest(&self) -> Manifest {
        Manifest {
            files: self
                .files
                .iter()
                .map(|(path, entry)| (path.clone(), entry.file.clone()))
                .collect(),
        }
    }

    pub fn author(
        &self,
        workspace: &str,
        base: &str,
        writer: Writer,
        edits: Vec<Edit>,
    ) -> Result<CausalBatch> {
        let updates = self.author_updates(&edits, &writer)?;
        Ok(CausalBatch {
            format: FORMAT.into(),
            workspace: workspace.into(),
            base: base.into(),
            writer,
            edits,
            updates,
        })
    }

    fn author_updates(&self, edits: &[Edit], writer: &Writer) -> Result<BTreeMap<String, Vec<u8>>> {
        writer.validate()?;
        if edits.is_empty()
            || edits.len() > 128
            || edits.iter().map(|e| e.insert.len()).sum::<usize>() > MAX_FILE
        {
            return Err(Error::Invalid(
                "batch must contain 1..128 edits, at most 16 MiB inserted".into(),
            ));
        }
        let mut grouped: BTreeMap<&str, Vec<&Edit>> = BTreeMap::new();
        for edit in edits {
            validate_path(&edit.path)?;
            grouped.entry(&edit.path).or_default().push(edit);
        }
        let mut updates = BTreeMap::new();
        for (path, edits) in grouped {
            let entry = self
                .files
                .get(path)
                .ok_or_else(|| Error::Missing(path.into()))?;
            let original = entry
                .doc
                .as_ref()
                .ok_or_else(|| Error::Unsupported(format!("{path} is not UTF-8 regular text")))?;
            let d = doc(writer.client);
            import(&d, &export(original))?;
            let text = d.get_or_insert_text("source");
            let mut txn = d.transact_mut();
            for edit in edits {
                let value = text.get_string(&txn);
                let start = edit.start as usize;
                let end = edit.end as usize;
                if start > end
                    || end > value.len()
                    || !value.is_char_boundary(start)
                    || !value.is_char_boundary(end)
                {
                    return Err(Error::Invalid(format!(
                        "invalid UTF-8 byte range for {path}"
                    )));
                }
                if value.len() - (end - start) + edit.insert.len() > MAX_FILE {
                    return Err(Error::Unsupported("result exceeds 16 MiB".into()));
                }
                if start == end && edit.insert.is_empty() {
                    return Err(Error::Invalid("empty edit".into()));
                }
                if end > start {
                    text.remove_range(&mut txn, edit.start, edit.end - edit.start);
                }
                if !edit.insert.is_empty() {
                    text.insert(&mut txn, edit.start, &edit.insert);
                }
            }
            updates.insert(path.into(), txn.encode_update_v1());
        }
        Ok(updates)
    }

    pub fn validate_batch(&self, batch: &CausalBatch, workspace: &str) -> Result<()> {
        if batch.format != FORMAT || batch.workspace != workspace {
            return Err(Error::Invalid("batch format or workspace mismatch".into()));
        }
        // Regenerate from the declared original base. Arbitrary engine bytes never reach apply.
        if self.author_updates(&batch.edits, &batch.writer)? != batch.updates {
            return Err(Error::Invalid(
                "native payload differs from declared edits and writer".into(),
            ));
        }
        Ok(())
    }

    fn apply_updates(&mut self, updates: &BTreeMap<String, Vec<u8>>) -> Result<()> {
        for (path, update) in updates {
            let entry = self
                .files
                .get_mut(path)
                .ok_or_else(|| Error::Missing(path.clone()))?;
            let doc = entry
                .doc
                .as_ref()
                .ok_or_else(|| Error::Unsupported(format!("{path} is opaque")))?;
            import(doc, update)?;
            entry.file.bytes = text_bytes(doc);
            if entry.file.bytes.len() > MAX_FILE {
                return Err(Error::Unsupported("result exceeds 16 MiB".into()));
            }
        }
        Ok(())
    }

    pub fn apply_batch(&mut self, batch: &CausalBatch) -> Result<()> {
        self.apply_updates(&batch.updates)
    }

    pub fn apply_record(&mut self, body: Body) -> Result<()> {
        match body {
            Body::Seed(files) => {
                if !self.files.is_empty() {
                    return Err(Error::Corrupt("seed after initial source".into()));
                }
                for (path, file) in files {
                    validate_path(&path)?;
                    self.files.insert(path, Entry::from_native(file)?);
                }
            }
            Body::Import { put, remove } => {
                for path in remove {
                    self.files.remove(&path);
                }
                for (path, file) in put {
                    validate_path(&path)?;
                    self.files.insert(path, Entry::from_native(file)?);
                }
            }
            Body::Edit(batch) => self.apply_updates(&batch.updates)?,
        }
        Ok(())
    }
}

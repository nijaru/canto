use crate::{
    Attachment, CausalBatch, Edit, Error, Manifest, Receipt, Request, Result, RevisionInfo, git,
    source::{Body, FORMAT, MAX_RECORD, Record, State, Writer},
    storage::{self, Store},
};
use rusqlite::{OptionalExtension, TransactionBehavior};
use std::{fs, path::Path};
use uuid::Uuid;

/// One application boundary for in-process clients and the CLI. SQLite serializes
/// local commits across processes. Gate A has no daemon or network listener.
pub struct Workspace {
    store: Store,
}

impl Workspace {
    /// Retains HEAD blobs and tracked/nonignored working files. Reads only; never
    /// resets files, writes the index, or imports an unknown editor's dirty buffers.
    pub fn attach(repository: &Path, directory: &Path) -> Result<(Self, Attachment)> {
        #[cfg(unix)]
        {
            use std::os::unix::fs::DirBuilderExt;
            fs::DirBuilder::new()
                .recursive(true)
                .mode(0o700)
                .create(directory)?;
        }
        #[cfg(not(unix))]
        fs::create_dir_all(directory)?;
        let directory = directory.canonicalize()?;
        if directory.join("workspace.sqlite3").exists() {
            return Err(Error::Invalid(
                "store already exists; open it instead".into(),
            ));
        }
        let observed = git::observe(repository, &directory)?;
        let workspace = Uuid::new_v4().to_string();
        let seed_writer = Writer::new("git-base");
        let (mut state, seed) = State::seed(observed.base, &seed_writer)?;
        let import_writer = Writer::new("observed-files");
        let imported = state.capture(observed.working, &import_writer)?;
        let base_record = Record {
            format: FORMAT.into(),
            workspace: workspace.clone(),
            parent: None,
            writer: seed_writer,
            body: seed,
        };
        // Validate encoding limits before reserving a persistent store.
        if serde_json::to_vec(&base_record)?.len() > MAX_RECORD
            || serde_json::to_vec(&imported)?.len() > MAX_RECORD / 2
        {
            return Err(Error::Unsupported(
                "capture too large for experimental store".into(),
            ));
        }
        let mut store = Store::create(&directory)?;
        let tx = store
            .conn
            .transaction_with_behavior(TransactionBehavior::Immediate)?;
        let base = storage::insert_record(&tx, &base_record)?;
        let retained = storage::insert_record(
            &tx,
            &Record {
                format: FORMAT.into(),
                workspace: workspace.clone(),
                parent: Some(base.clone()),
                writer: import_writer,
                body: imported,
            },
        )?;
        let attachment = Attachment {
            workspace,
            repository: observed
                .root
                .to_str()
                .ok_or_else(|| Error::Unsupported("non-UTF-8 repository root".into()))?
                .into(),
            git_head: observed.head,
            base,
            retained,
        };
        tx.execute(
            "INSERT INTO metadata VALUES ('attachment',?1)",
            [serde_json::to_vec(&attachment)?],
        )?;
        tx.execute(
            "INSERT INTO workstreams VALUES ('main',?1)",
            [&attachment.retained],
        )?;
        tx.commit()?;
        Ok((Self { store }, attachment))
    }

    pub fn open(directory: &Path) -> Result<Self> {
        Ok(Self {
            store: Store::open(directory)?,
        })
    }
    pub fn attachment(&self) -> Result<Attachment> {
        storage::attachment(&self.store.conn)
    }
    pub fn head(&self, workstream: &str) -> Result<String> {
        storage::head(&self.store.conn, workstream)
    }
    pub fn read(&self, revision: &str) -> Result<Manifest> {
        Ok(
            storage::reconstruct(&self.store.conn, revision, &self.attachment()?.workspace)?
                .manifest(),
        )
    }

    /// Fork only source history. No checkout is provisioned or changed.
    pub fn fork(&mut self, revision: &str, name: &str) -> Result<Receipt> {
        if name.is_empty()
            || name.len() > 128
            || name.contains('/')
            || name.chars().any(char::is_control)
        {
            return Err(Error::Invalid("workstream name".into()));
        }
        let tx = self
            .store
            .conn
            .transaction_with_behavior(TransactionBehavior::Immediate)?;
        let workspace = storage::attachment(&tx)?.workspace;
        storage::record(&tx, revision, &workspace)?;
        let prior: Option<String> = tx
            .query_row("SELECT head FROM workstreams WHERE name=?1", [name], |r| {
                r.get(0)
            })
            .optional()?;
        if let Some(head) = prior {
            if head != revision {
                return Err(Error::Invalid(
                    "workstream already exists at a different revision".into(),
                ));
            }
        } else {
            tx.execute("INSERT INTO workstreams VALUES (?1,?2)", [name, revision])?;
        }
        tx.commit()?;
        Ok(Receipt {
            revision: revision.into(),
            workstream: name.into(),
            recorded: true,
        })
    }

    /// Authors from exactly the declared revision, not the current coordinator head.
    /// The returned immutable batch can be carried to a later local admission.
    pub fn author(&self, base: &str, actor: &str, edits: Vec<Edit>) -> Result<CausalBatch> {
        let workspace = self.attachment()?.workspace;
        storage::reconstruct(&self.store.conn, base, &workspace)?.author(
            &workspace,
            base,
            Writer::new(actor),
            edits,
        )
    }

    /// Whole-head conditional mutation; all edits commit together or none do.
    pub fn apply(
        &mut self,
        stream: &str,
        expected: &str,
        actor: &str,
        edits: Vec<Edit>,
        request: Request,
    ) -> Result<Receipt> {
        let fingerprint = storage::fingerprint(&("conditional", stream, expected, actor, &edits))?;
        let tx = self
            .store
            .conn
            .transaction_with_behavior(TransactionBehavior::Immediate)?;
        if let Some(receipt) = storage::prior(&tx, &request, &fingerprint)? {
            return Ok(receipt);
        }
        let current = storage::head(&tx, stream)?;
        if current != expected {
            return Err(Error::Stale {
                expected: expected.into(),
                actual: current,
            });
        }
        let workspace = storage::attachment(&tx)?.workspace;
        let mut state = storage::reconstruct(&tx, &current, &workspace)?;
        let batch = state.author(&workspace, expected, Writer::new(actor), edits)?;
        let writer = batch.writer.clone();
        // Only ephemeral state changes before the complete batch commits.
        state.apply_batch(&batch)?;
        let body = Body::Edit(batch);
        let revision = storage::insert_record(
            &tx,
            &Record {
                format: FORMAT.into(),
                workspace,
                parent: Some(current),
                writer,
                body,
            },
        )?;
        let receipt = storage::finish(&tx, stream, revision, &request, &fingerprint)?;
        tx.commit()?;
        Ok(receipt)
    }

    /// Explicit collaborative admission, not independent-task integration. The
    /// original base must already be included in the selected workstream.
    pub fn admit(&mut self, stream: &str, batch: CausalBatch, request: Request) -> Result<Receipt> {
        if serde_json::to_vec(&batch)?.len() > MAX_RECORD {
            return Err(Error::Invalid("batch too large".into()));
        }
        let fingerprint = storage::fingerprint(&("causal", stream, &batch))?;
        let tx = self
            .store
            .conn
            .transaction_with_behavior(TransactionBehavior::Immediate)?;
        if let Some(receipt) = storage::prior(&tx, &request, &fingerprint)? {
            return Ok(receipt);
        }
        let workspace = storage::attachment(&tx)?.workspace;
        let current = storage::head(&tx, stream)?;
        if !storage::records(&tx, &current, &workspace)?
            .iter()
            .any(|(id, _)| id == &batch.base)
        {
            return Err(Error::Invalid(
                "authored base is not included in the target workstream".into(),
            ));
        }
        storage::reconstruct(&tx, &batch.base, &workspace)?.validate_batch(&batch, &workspace)?;
        let writer = batch.writer.clone();
        let mut state = storage::reconstruct(&tx, &current, &workspace)?;
        state.apply_batch(&batch)?;
        let body = Body::Edit(batch);
        let revision = storage::insert_record(
            &tx,
            &Record {
                format: FORMAT.into(),
                workspace,
                parent: Some(current),
                writer,
                body,
            },
        )?;
        let receipt = storage::finish(&tx, stream, revision, &request, &fingerprint)?;
        tx.commit()?;
        Ok(receipt)
    }

    pub fn history(&self, revision: &str) -> Result<Vec<RevisionInfo>> {
        Ok(
            storage::records(&self.store.conn, revision, &self.attachment()?.workspace)?
                .into_iter()
                .map(|(revision, record)| {
                    let authored_base = if let Body::Edit(batch) = record.body {
                        Some(batch.base)
                    } else {
                        None
                    };
                    RevisionInfo {
                        revision,
                        parent: record.parent,
                        authored_base,
                        writer_epoch: record.writer.epoch,
                        actor: record.writer.actor,
                    }
                })
                .collect(),
        )
    }
}

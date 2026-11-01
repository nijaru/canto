//! The only durable commit owner. All source payloads and heads share SQLite transactions.
use crate::{
    Attachment, Error, Receipt, Request, Result,
    source::{Body, FORMAT, MAX_RECORD, Record, State},
};
use rusqlite::{Connection, OpenFlags, OptionalExtension};
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::{collections::HashSet, fs, path::Path, time::Duration};

const APPLICATION_ID: i32 = 0x43414e54;
const VERSION: i32 = 1;
pub(crate) struct Store {
    pub conn: Connection,
}

pub(crate) fn digest(bytes: &[u8]) -> String {
    format!("{:x}", Sha256::digest(bytes))
}
pub(crate) fn fingerprint(value: &impl Serialize) -> Result<String> {
    Ok(digest(&serde_json::to_vec(value)?))
}

fn configure(conn: &Connection) -> Result<()> {
    conn.busy_timeout(Duration::from_secs(5))?;
    conn.pragma_update(None, "foreign_keys", true)?;
    conn.pragma_update(None, "synchronous", "FULL")?;
    Ok(())
}

impl Store {
    pub fn create(directory: &Path) -> Result<Self> {
        let path = directory.join("workspace.sqlite3");
        // Reserve the filename without replacing a previous workspace, even if attach later fails.
        fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&path)?;
        let conn = Connection::open(&path)?;
        configure(&conn)?;
        conn.pragma_update(None, "journal_mode", "WAL")?;
        conn.execute_batch(&format!("BEGIN IMMEDIATE;
            PRAGMA application_id={APPLICATION_ID}; PRAGMA user_version={VERSION};
            CREATE TABLE metadata (key TEXT PRIMARY KEY, value BLOB NOT NULL) STRICT;
            CREATE TABLE revisions (id TEXT PRIMARY KEY, payload BLOB NOT NULL) STRICT;
            CREATE TABLE writers (epoch TEXT PRIMARY KEY, client INTEGER UNIQUE NOT NULL) STRICT;
            CREATE TABLE workstreams (name TEXT PRIMARY KEY, head TEXT NOT NULL REFERENCES revisions(id)) STRICT;
            CREATE TABLE requests (client TEXT NOT NULL, id TEXT NOT NULL, fingerprint TEXT NOT NULL, receipt BLOB NOT NULL, PRIMARY KEY(client,id)) STRICT;
            COMMIT;"))?;
        Ok(Self { conn })
    }

    pub fn open(directory: &Path) -> Result<Self> {
        let conn = Connection::open_with_flags(
            directory.join("workspace.sqlite3"),
            OpenFlags::SQLITE_OPEN_READ_WRITE,
        )?;
        let app: i32 = conn.pragma_query_value(None, "application_id", |r| r.get(0))?;
        let version: i32 = conn.pragma_query_value(None, "user_version", |r| r.get(0))?;
        if app != APPLICATION_ID || version != VERSION {
            return Err(Error::Corrupt("unknown experimental schema".into()));
        }
        configure(&conn)?;
        attachment(&conn)?;
        Ok(Self { conn })
    }
}

pub(crate) fn attachment(conn: &Connection) -> Result<Attachment> {
    let bytes: Vec<u8> = conn.query_row(
        "SELECT value FROM metadata WHERE key='attachment'",
        [],
        |r| r.get(0),
    )?;
    Ok(serde_json::from_slice(&bytes)?)
}

pub(crate) fn head(conn: &Connection, stream: &str) -> Result<String> {
    conn.query_row("SELECT head FROM workstreams WHERE name=?", [stream], |r| {
        r.get(0)
    })
    .optional()?
    .ok_or_else(|| Error::Missing(format!("workstream {stream}")))
}

pub(crate) fn record(conn: &Connection, id: &str, workspace: &str) -> Result<Record> {
    if id.len() != 64 || !id.bytes().all(|b| b.is_ascii_hexdigit()) {
        return Err(Error::Invalid("revision identity".into()));
    }
    let bytes: Vec<u8> = conn
        .query_row("SELECT payload FROM revisions WHERE id=?", [id], |r| {
            r.get(0)
        })
        .optional()?
        .ok_or_else(|| Error::Missing(format!("revision {id}")))?;
    if bytes.len() > MAX_RECORD || digest(&bytes) != id {
        return Err(Error::Corrupt(format!("revision integrity: {id}")));
    }
    let record: Record = serde_json::from_slice(&bytes)?;
    if record.format != FORMAT || record.workspace != workspace {
        return Err(Error::Corrupt("record format/workspace".into()));
    }
    record
        .writer
        .validate()
        .map_err(|e| Error::Corrupt(e.to_string()))?;
    Ok(record)
}

pub(crate) fn records(
    conn: &Connection,
    revision: &str,
    workspace: &str,
) -> Result<Vec<(String, Record)>> {
    let mut chain = Vec::new();
    let mut next = Some(revision.to_owned());
    let mut seen = HashSet::new();
    while let Some(id) = next {
        if !seen.insert(id.clone()) || chain.len() >= 100000 {
            return Err(Error::Corrupt("cyclic or excessive history".into()));
        }
        let item = record(conn, &id, workspace)?;
        next = item.parent.clone();
        chain.push((id, item));
    }
    chain.reverse();
    Ok(chain)
}

pub(crate) fn reconstruct(conn: &Connection, revision: &str, workspace: &str) -> Result<State> {
    let mut state = State::default();
    let mut seen = HashSet::new();
    for (index, (id, record)) in records(conn, revision, workspace)?.into_iter().enumerate() {
        if matches!(record.body, Body::Seed(_)) != (index == 0) {
            return Err(Error::Corrupt("invalid seed position".into()));
        }
        if let Body::Edit(batch) = &record.body
            && (!seen.contains(&batch.base)
                || batch.writer != record.writer
                || batch.workspace != workspace
                || batch.format != FORMAT)
        {
            return Err(Error::Corrupt("invalid causal envelope".into()));
        }
        state.apply_record(record.body)?;
        seen.insert(id);
    }
    Ok(state)
}

pub(crate) fn insert_record(conn: &Connection, record: &Record) -> Result<String> {
    let bytes = serde_json::to_vec(record)?;
    if bytes.len() > MAX_RECORD {
        return Err(Error::Unsupported("encoded record exceeds 64 MiB".into()));
    }
    record.writer.validate()?;
    let client = i64::try_from(record.writer.client)
        .map_err(|_| Error::Invalid("writer client ID".into()))?;
    let id = digest(&bytes);
    let exists: bool = conn.query_row(
        "SELECT EXISTS(SELECT 1 FROM writers WHERE epoch=?1 OR client=?2)",
        rusqlite::params![record.writer.epoch, client],
        |r| r.get(0),
    )?;
    if exists {
        return Err(Error::WriterConflict);
    }
    conn.execute(
        "INSERT INTO writers VALUES (?1,?2)",
        rusqlite::params![record.writer.epoch, client],
    )?;
    conn.execute(
        "INSERT INTO revisions VALUES (?1,?2)",
        rusqlite::params![id, bytes],
    )?;
    Ok(id)
}

pub(crate) fn prior(
    conn: &Connection,
    request: &Request,
    fingerprint: &str,
) -> Result<Option<Receipt>> {
    for value in [&request.client, &request.id] {
        if value.is_empty() || value.len() > 256 {
            return Err(Error::Invalid("request identity".into()));
        }
    }
    let row: Option<(String, Vec<u8>)> = conn
        .query_row(
            "SELECT fingerprint,receipt FROM requests WHERE client=?1 AND id=?2",
            rusqlite::params![request.client, request.id],
            |r| Ok((r.get(0)?, r.get(1)?)),
        )
        .optional()?;
    match row {
        Some((stored, bytes)) if stored == fingerprint => Ok(Some(serde_json::from_slice(&bytes)?)),
        Some(_) => Err(Error::RequestConflict),
        None => Ok(None),
    }
}

pub(crate) fn finish(
    conn: &Connection,
    stream: &str,
    revision: String,
    request: &Request,
    fingerprint: &str,
) -> Result<Receipt> {
    conn.execute(
        "UPDATE workstreams SET head=?1 WHERE name=?2",
        rusqlite::params![revision, stream],
    )?;
    let receipt = Receipt {
        revision,
        workstream: stream.into(),
        recorded: true,
    };
    conn.execute(
        "INSERT INTO requests VALUES (?1,?2,?3,?4)",
        rusqlite::params![
            request.client,
            request.id,
            fingerprint,
            serde_json::to_vec(&receipt)?
        ],
    )?;
    Ok(receipt)
}

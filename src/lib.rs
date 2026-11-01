//! An experimental local source workspace. All clients use this application API.
//! No stable storage or replication protocol is promised.
#![forbid(unsafe_code)]

mod application;
mod checkout;
mod git;
mod source;
mod storage;

pub use application::Workspace;
use serde::{Deserialize, Serialize};
pub use source::{CausalBatch, Edit, File, FileKind, Manifest};

pub type Result<T> = std::result::Result<T, Error>;

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error(transparent)]
    Io(#[from] std::io::Error),
    #[error(transparent)]
    Sql(#[from] rusqlite::Error),
    #[error(transparent)]
    Json(#[from] serde_json::Error),
    #[error("Git: {0}")]
    Git(String),
    #[error("unsupported source: {0}")]
    Unsupported(String),
    #[error("invalid input: {0}")]
    Invalid(String),
    #[error("missing {0}")]
    Missing(String),
    #[error("stale base: expected {expected}, actual {actual}")]
    Stale { expected: String, actual: String },
    #[error("request identity was reused with different input")]
    RequestConflict,
    #[error("causal writer identity has already been used")]
    WriterConflict,
    #[error("corrupt or incompatible store: {0}")]
    Corrupt(String),
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct Attachment {
    pub workspace: String,
    pub repository: String,
    pub git_head: Option<String>,
    pub base: String,
    pub retained: String,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq, Eq)]
pub struct Receipt {
    pub revision: String,
    pub workstream: String,
    /// Source is recorded. Gate A never installs files or publishes Git changes.
    pub recorded: bool,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct Request {
    pub client: String,
    pub id: String,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct RevisionInfo {
    pub revision: String,
    /// Source inclusion parent, not a claim that the causal writer observed it.
    pub parent: Option<String>,
    pub authored_base: Option<String>,
    pub writer_epoch: String,
    pub actor: String,
}

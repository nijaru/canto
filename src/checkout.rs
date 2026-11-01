//! Descriptor-relative, no-follow capture. This is observation, not materialization.
use crate::{
    Error, Result,
    source::{FileKind, MAX_FILE, validate_path},
};
use std::path::Path;

pub(crate) struct ObservedFile {
    pub kind: FileKind,
    pub executable: bool,
    pub bytes: Vec<u8>,
}

#[cfg(unix)]
pub(crate) struct Files {
    root: rustix::fd::OwnedFd,
}

#[cfg(unix)]
impl Files {
    pub fn open(root: &Path) -> Result<Self> {
        use rustix::fs::{Mode, OFlags, open};
        Ok(Self {
            root: open(
                root,
                OFlags::RDONLY | OFlags::DIRECTORY | OFlags::NOFOLLOW | OFlags::CLOEXEC,
                Mode::empty(),
            )
            .map_err(std::io::Error::from)?,
        })
    }

    pub fn read(&self, path: &str) -> Result<Option<ObservedFile>> {
        use rustix::{
            fd::AsFd,
            fs::{AtFlags, FileType, Mode, OFlags, fstat, openat, readlinkat, statat},
            io::Errno,
        };
        use std::{fs::File, io::Read};
        validate_path(path)?;
        let parts: Vec<_> = path.split('/').collect();
        let mut parent = None;
        for part in &parts[..parts.len() - 1] {
            let fd = parent.as_ref().unwrap_or(&self.root);
            match openat(
                fd,
                *part,
                OFlags::RDONLY | OFlags::DIRECTORY | OFlags::NOFOLLOW | OFlags::CLOEXEC,
                Mode::empty(),
            ) {
                Ok(fd) => parent = Some(fd),
                Err(Errno::NOENT) => return Ok(None),
                Err(Errno::LOOP | Errno::NOTDIR) => {
                    return Err(Error::Unsupported(format!(
                        "symlink or non-directory ancestor at {path}"
                    )));
                }
                Err(e) => return Err(std::io::Error::from(e).into()),
            }
        }
        let parent = parent.as_ref().unwrap_or(&self.root).as_fd();
        let name = parts[parts.len() - 1];
        let stat = match statat(parent, name, AtFlags::SYMLINK_NOFOLLOW) {
            Ok(stat) => stat,
            Err(Errno::NOENT) => return Ok(None),
            Err(e) => return Err(std::io::Error::from(e).into()),
        };
        match FileType::from_raw_mode(stat.st_mode) {
            FileType::Symlink => {
                let target = readlinkat(parent, name, Vec::new()).map_err(std::io::Error::from)?;
                Ok(Some(ObservedFile {
                    kind: FileKind::Symlink,
                    executable: false,
                    bytes: target.into_bytes(),
                }))
            }
            FileType::RegularFile => {
                // NOFOLLOW closes the lstat/open race; NONBLOCK prevents a swapped-in
                // FIFO from blocking before fstat can reject its actual file type.
                let fd = openat(
                    parent,
                    name,
                    OFlags::RDONLY | OFlags::NOFOLLOW | OFlags::NONBLOCK | OFlags::CLOEXEC,
                    Mode::empty(),
                )
                .map_err(std::io::Error::from)?;
                let stat = fstat(&fd).map_err(std::io::Error::from)?;
                if FileType::from_raw_mode(stat.st_mode) != FileType::RegularFile {
                    return Err(Error::Unsupported(format!("file type changed at {path}")));
                }
                let mut bytes = Vec::new();
                File::from(fd)
                    .take((MAX_FILE + 1) as u64)
                    .read_to_end(&mut bytes)?;
                Ok(Some(ObservedFile {
                    kind: FileKind::Regular,
                    executable: stat.st_mode & 0o111 != 0,
                    bytes,
                }))
            }
            _ => Err(Error::Unsupported(format!(
                "special file or submodule at {path}"
            ))),
        }
    }
}

#[cfg(not(unix))]
pub(crate) struct Files;
#[cfg(not(unix))]
impl Files {
    pub fn open(_: &Path) -> Result<Self> {
        Err(Error::Unsupported("capture currently requires Unix".into()))
    }
    pub fn read(&self, _: &str) -> Result<Option<ObservedFile>> {
        Err(Error::Unsupported("capture currently requires Unix".into()))
    }
}

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use std::{
        fs,
        os::unix::fs::symlink,
        sync::{
            Arc, Barrier,
            atomic::{AtomicBool, Ordering},
        },
    };

    #[test]
    fn concurrent_symlink_swaps_never_import_outside_file_bytes() {
        let repo = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        let secret = outside.path().join("secret");
        fs::write(&secret, b"outside-private").unwrap();
        let path = repo.path().join("source");
        fs::write(&path, b"inside").unwrap();
        let files = Files::open(repo.path()).unwrap();
        let stop = Arc::new(AtomicBool::new(false));
        let ready = Arc::new(Barrier::new(2));
        let handle = {
            let stop = stop.clone();
            let ready = ready.clone();
            let stage = repo.path().join("stage");
            std::thread::spawn(move || {
                ready.wait();
                while !stop.load(Ordering::Relaxed) {
                    fs::write(&stage, b"inside").unwrap();
                    fs::rename(&stage, &path).unwrap();
                    symlink(&secret, &stage).unwrap();
                    fs::rename(&stage, &path).unwrap();
                }
            })
        };
        ready.wait();
        let mut leaked = None;
        let mut regular_reads = 0;
        for _ in 0..4000 {
            if let Ok(Some(observed)) = files.read("source")
                && observed.kind == FileKind::Regular
            {
                regular_reads += 1;
                if observed.bytes != b"inside" {
                    leaked = Some(observed.bytes);
                    break;
                }
            }
        }
        stop.store(true, Ordering::Relaxed);
        handle.join().unwrap();
        assert!(regular_reads > 0);
        assert_eq!(leaked, None, "a symlink race crossed the capture boundary");
    }
}

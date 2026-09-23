#!/usr/bin/env python3
"""Validate a v4 release tar layout and safely extract it for local preflight."""
from __future__ import annotations

import argparse
import os
from pathlib import Path
import re
import shutil
import stat
import sys
import tarfile
from typing import NamedTuple


class ArchiveLayoutError(ValueError):
    """The release archive does not meet the flat-root safety contract."""


class ValidatedMember(NamedTuple):
    info: tarfile.TarInfo
    parts: tuple[str, ...]
    is_directory: bool


def _member_path(info: tarfile.TarInfo) -> tuple[tuple[str, ...], bool]:
    name = info.name
    if not name or "\x00" in name or "\\" in name:
        raise ArchiveLayoutError("archive contains an empty or non-portable member path")
    if name.startswith("/") or re.match(r"^[A-Za-z]:", name):
        raise ArchiveLayoutError(f"archive contains an absolute member path: {name!r}")
    if any(ord(char) < 32 for char in name):
        raise ArchiveLayoutError("archive contains a control character in a member path")

    is_directory = info.isdir()
    normalized = name[:-1] if is_directory and name.endswith("/") else name
    parts = tuple(normalized.split("/"))
    if not normalized or any(part in {"", ".", ".."} for part in parts):
        raise ArchiveLayoutError(f"archive contains an unsafe member path: {name!r}")
    if any(part.startswith("._") for part in parts):
        raise ArchiveLayoutError(f"archive contains an AppleDouble member: {name!r}")
    if is_directory and info.type != tarfile.DIRTYPE:
        raise ArchiveLayoutError(f"archive contains a non-regular member: {name!r}")
    if not is_directory and info.type not in (tarfile.REGTYPE, tarfile.AREGTYPE):
        raise ArchiveLayoutError(f"archive contains a non-regular member: {name!r}")
    if info.mode & (stat.S_ISUID | stat.S_ISGID | stat.S_ISVTX):
        raise ArchiveLayoutError(f"archive contains special permission bits: {name!r}")
    if info.size < 0:
        raise ArchiveLayoutError(f"archive contains an invalid member size: {name!r}")
    return parts, is_directory


def _validate_members(bundle: tarfile.TarFile) -> list[ValidatedMember]:
    members: list[ValidatedMember] = []
    by_path: dict[tuple[str, ...], ValidatedMember] = {}
    for info in bundle.getmembers():
        parts, is_directory = _member_path(info)
        member = ValidatedMember(info, parts, is_directory)
        if parts in by_path:
            raise ArchiveLayoutError(f"archive contains a duplicate member path: {info.name!r}")
        by_path[parts] = member
        members.append(member)

    for member in members:
        for length in range(1, len(member.parts)):
            parent = by_path.get(member.parts[:length])
            if parent and not parent.is_directory:
                raise ArchiveLayoutError(
                    f"archive file is also used as a parent directory: {'/'.join(parent.parts)!r}"
                )
        if not member.is_directory and any(
            len(path) > len(member.parts) and path[:len(member.parts)] == member.parts
            for path in by_path
        ):
            raise ArchiveLayoutError(
                f"archive file conflicts with a nested member: {'/'.join(member.parts)!r}"
            )

    regular_files = [member for member in members if not member.is_directory]
    root_files = {member.parts for member in regular_files if len(member.parts) == 1}
    for root in ("bin", "migrations"):
        if not any(member.parts[0] == root and len(member.parts) > 1 for member in regular_files):
            raise ArchiveLayoutError(f"release archive must contain files under root {root}/")
    if not any(member.parts[0] == "bin" and len(member.parts) > 1 for member in regular_files):
        raise ArchiveLayoutError("release archive root bin/ is empty")
    if not any(
        member.parts[0] == "migrations"
        and member.parts[-1].endswith(".sql")
        for member in regular_files
    ):
        raise ArchiveLayoutError("release archive root migrations/ contains no SQL migrations")
    if ("release-files.sha256",) not in root_files:
        raise ArchiveLayoutError("release archive is missing root release-files.sha256")
    return members


def prepare_release_archive(archive_path: Path, output_root: Path) -> int:
    """Validate every tar member before extracting files beneath output_root."""
    if output_root.is_symlink():
        raise ArchiveLayoutError("preflight output root must not be a symlink")
    try:
        output_root.mkdir(parents=True, exist_ok=True, mode=0o700)
    except OSError as error:
        raise ArchiveLayoutError(f"cannot prepare preflight output root: {error}") from error
    if not output_root.is_dir() or any(output_root.iterdir()):
        raise ArchiveLayoutError("preflight output root must be an empty directory")

    try:
        with tarfile.open(archive_path, mode="r:gz") as bundle:
            members = _validate_members(bundle)
            directories = {
                member.parts[:length]
                for member in members
                for length in range(1, len(member.parts) + 1)
                if member.is_directory or length < len(member.parts)
            }
            for parts in sorted(directories, key=lambda value: (len(value), value)):
                output_root.joinpath(*parts).mkdir(mode=0o700, exist_ok=True)

            extracted_files = 0
            for member in members:
                if member.is_directory:
                    continue
                destination = output_root.joinpath(*member.parts)
                source = bundle.extractfile(member.info)
                if source is None:
                    raise ArchiveLayoutError(
                        f"archive member cannot be read: {'/'.join(member.parts)!r}"
                    )
                mode = member.info.mode & 0o755
                try:
                    descriptor = os.open(
                        destination,
                        os.O_WRONLY | os.O_CREAT | os.O_EXCL | getattr(os, "O_NOFOLLOW", 0),
                        mode or 0o600,
                    )
                    with source, os.fdopen(descriptor, "wb") as target:
                        shutil.copyfileobj(source, target, length=1024 * 1024)
                    os.chmod(destination, mode or 0o600)
                except FileExistsError as error:
                    raise ArchiveLayoutError(
                        f"archive member collides with an existing path: {'/'.join(member.parts)!r}"
                    ) from error
                if destination.stat().st_size != member.info.size:
                    raise ArchiveLayoutError(
                        f"archive member has an unexpected size: {'/'.join(member.parts)!r}"
                    )
                extracted_files += 1
            return extracted_files
    except (OSError, tarfile.TarError) as error:
        raise ArchiveLayoutError(f"cannot read or extract release archive: {error}") from error


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("archive", type=Path)
    parser.add_argument("output_root", type=Path)
    args = parser.parse_args()
    try:
        count = prepare_release_archive(args.archive, args.output_root)
    except ArchiveLayoutError as error:
        print(f"release archive rejected: {error}", file=sys.stderr)
        return 2
    print(f"release archive layout: {count} files extracted from flat archive root")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

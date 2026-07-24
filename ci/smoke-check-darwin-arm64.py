#!/usr/bin/env python3
"""Smoke-check a darwin/arm64 opencode-msb binary without executing it.

Asserts the file is a 64-bit Mach-O for arm64 and carries an
LC_CODE_SIGNATURE load command (required for arm64 macOS execution).
Exits 0 on success, 1 on any check failure, 2 on usage error. Stdlib only.
"""
import struct
import sys

MH_MAGIC_64 = 0xFEEDFACF
CPU_TYPE_ARM64 = 0x0100000C
LC_CODE_SIGNATURE = 0x1D
HEADER_SIZE = 32  # mach_header_64


def check(path: str) -> str:
    with open(path, "rb") as f:
        data = f.read(HEADER_SIZE)
        if len(data) < HEADER_SIZE:
            return f"{path}: too small to be a Mach-O ({len(data)} bytes)"
        magic, cputype, _cpusubtype, _filetype, ncmds, sizeofcmds, _flags, _reserved = (
            struct.unpack_from("<8I", data, 0)
        )
        if magic != MH_MAGIC_64:
            return f"{path}: not a 64-bit Mach-O (magic=0x{magic:08X})"
        if cputype != CPU_TYPE_ARM64:
            return f"{path}: not arm64 (cputype=0x{cputype:08X})"
        f.seek(HEADER_SIZE)
        body = f.read(sizeofcmds)
    off = 0
    for _ in range(ncmds):
        if off + 8 > len(body):
            return f"{path}: truncated load commands"
        cmd, cmdsize = struct.unpack_from("<2I", body, off)
        if cmd == LC_CODE_SIGNATURE:
            return ""
        if cmdsize < 8:
            return f"{path}: malformed load command (cmdsize={cmdsize})"
        off += cmdsize
    return f"{path}: missing LC_CODE_SIGNATURE (no embedded ad-hoc signature)"


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print(f"usage: {argv[0]} <mach-o-binary>", file=sys.stderr)
        return 2
    msg = check(argv[1])
    if msg:
        print(msg, file=sys.stderr)
        return 1
    print(f"{argv[1]}: OK (64-bit arm64 Mach-O with LC_CODE_SIGNATURE)")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))

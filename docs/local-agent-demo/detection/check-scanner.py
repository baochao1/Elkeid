#!/usr/bin/env python3
"""Exercise the published Linux scanner against two inert text files."""

import hashlib
import json
import os
from pathlib import Path
import select
import struct
import subprocess
import sys
import tarfile
import tempfile
import time
import zipfile


def varint(value):
    encoded = bytearray()
    while value >= 128:
        encoded.append((value & 127) | 128)
        value >>= 7
    encoded.append(value)
    return bytes(encoded)


def string_field(number, value):
    value = value.encode()
    return varint(8 * number + 2) + varint(len(value)) + value


def take_varint(data, offset):
    value = 0
    for shift in range(0, 70, 7):
        byte = data[offset]
        offset += 1
        value |= (byte & 127) << shift
        if byte < 128:
            return value, offset
    raise ValueError("invalid protobuf varint")


def protobuf_fields(data):
    offset = 0
    while offset < len(data):
        tag, offset = take_varint(data, offset)
        if tag & 7 == 0:
            value, offset = take_varint(data, offset)
        elif tag & 7 == 2:
            length, offset = take_varint(data, offset)
            value = data[offset:offset + length]
            offset += length
        else:
            raise ValueError(f"unexpected protobuf tag {tag}")
        yield tag >> 3, value


def read_exact(pipe, count, deadline):
    data = bytearray()
    while len(data) < count:
        timeout = max(0, deadline - time.monotonic())
        if not select.select([pipe], [], [], timeout)[0]:
            raise TimeoutError("waiting for scanner IPC response")
        chunk = os.read(pipe.fileno(), count - len(data))
        if not chunk:
            raise EOFError("scanner IPC closed")
        data.extend(chunk)
    return data


def prepare_fixtures(work):
    hit = b"Elkeid local AV demonstration marker 2026\n"
    targets = {"demo-hit": hit, "demo-clean": b"An unrelated local text fixture.\n"}
    signature = "Elkeid.LocalDemo.Marker"
    (work / "tmp").mkdir()
    archive = work / "tmp" / "fixture.zip"
    with zipfile.ZipFile(archive, "w") as database:
        database.writestr("version", "local-demo-1\n")
        database.writestr("marker.ndb", f"{signature}:0:*:{hit.hex()}\n")
    detail = {
        "version": "local-demo-1",
        "dbname": "fixture",
        "url": [],
        "sha256": hashlib.sha256(archive.read_bytes()).hexdigest(),
        "passwd": "",
    }
    for token, content in targets.items():
        (work / f"{token}.txt").write_bytes(content)
    return detail, signature, targets


def prepare_package(scanner, work):
    detail, _, _ = prepare_fixtures(work)
    package = work / "scanner-fixture.tar.gz"
    with tarfile.open(package, "w:gz") as archive:
        archive.add(scanner, arcname="scanner")
        archive.add(work / "tmp" / "fixture.zip", arcname="tmp/fixture.zip")
    (work / "detail.json").write_text(json.dumps(detail), encoding="utf-8")
    print(f"Scanner package: {package}")
    print(f"Archive SHA-256: {hashlib.sha256(package.read_bytes()).hexdigest()}")
    print(f"Scanner SHA-256: {hashlib.sha256(scanner.read_bytes()).hexdigest()}")


def scan(scanner, work):
    detail, signature, targets = prepare_fixtures(work)
    with subprocess.Popen(
        ["bash", "-c", 'exec 3<&0 4>&1 5>&1; exec "$1"', "scanner", scanner],
        cwd=work,
        env={**os.environ, "DETAIL": json.dumps(detail)},
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
    ) as child:
        try:
            for token in targets:
                target = work / f"{token}.txt"
                message = (
                    b"\x08" + varint(6053)
                    + string_field(2, "scanner")
                    + string_field(3, json.dumps({"exe": str(target)}))
                    + string_field(4, token)
                )
                child.stdin.write(struct.pack("<I", len(message)) + message)
            child.stdin.flush()
            reports = {}
            deadline = time.monotonic() + 30
            while len(reports) < len(targets):
                size, = struct.unpack("<I", read_exact(child.stdout, 4, deadline))
                if size > 1024 * 1024:
                    raise ValueError(f"unexpected scanner record size {size}")
                record = dict(protobuf_fields(read_exact(child.stdout, size, deadline)))
                if record.get(1) != 6003:
                    continue
                fields = {}
                for _, entry in protobuf_fields(record[3]):
                    pair = dict(protobuf_fields(entry))
                    fields[pair[1].decode()] = pair.get(2, b"").decode()
                token = fields.get("token")
                if token in targets:
                    print(json.dumps({"data_type": 6003, **fields}, sort_keys=True), flush=True)
                    reports[token] = fields
            if any(result.get("error") for result in reports.values()):
                raise RuntimeError("scanner returned a task error")
            positive = reports["demo-hit"]
            detection = ".".join(positive.get(field, "") for field in ("types", "class", "name"))
            if detection != signature:
                raise RuntimeError("expected marker signature on demo-hit")
            if reports["demo-clean"].get("types") != "not_detected":
                raise RuntimeError("expected demo-clean to remain undetected")
        finally:
            child.kill()


if __name__ == "__main__":
    if len(sys.argv) == 4 and sys.argv[1] == "--prepare":
        prepare_package(Path(sys.argv[2]).resolve(strict=True), Path(sys.argv[3]).resolve(strict=True))
    elif len(sys.argv) == 2:
        scanner = Path(sys.argv[1]).resolve(strict=True)
        with tempfile.TemporaryDirectory(prefix="elkeid-av-") as workspace:
            scan(scanner, Path(workspace))
    else:
        sys.exit(f"Usage: {sys.argv[0]} [--prepare] /absolute/path/to/scanner [empty-directory]")

#!/usr/bin/env python3
"""Build an enabled-v1 program/release pair from C1 overlay bytes.

Prepare must reject the program (redirect-target-v1 + enabled). Weights
stay the C1 file. Writes program.json / weights.json / release.json and
prints sha/size for pin env. Does not upload.
"""
from __future__ import annotations

import hashlib
import json
import shutil
import sys
from pathlib import Path


def sha256(p: Path) -> str:
    return hashlib.sha256(p.read_bytes()).hexdigest()


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: traffic-runtime-make-incompat.py <c1-overlay-dir> <out-dir>", file=sys.stderr)
        return 2
    src = Path(sys.argv[1])
    out = Path(sys.argv[2])
    out.mkdir(parents=True, exist_ok=True)
    prog = json.loads((src / "program.json").read_text())
    changed = 0
    for v in prog.get("variants") or []:
        if v.get("typeId") != "redirect-target":
            continue
        v["schemaVersion"] = 1
        v["handlerVersion"] = "redirect-target-v1"
        payload = v.get("payload") or {}
        if isinstance(payload, str):
            payload = json.loads(payload)
        payload["enabled"] = True
        payload.pop("url", None)
        v["payload"] = payload
        changed += 1
    if changed == 0:
        print("no redirect-target variants to flip", file=sys.stderr)
        return 1
    (out / "program.json").write_text(json.dumps(prog, separators=(",", ":")), encoding="utf-8")
    shutil.copyfile(src / "weights.json", out / "weights.json")
    rel = json.loads((src / "release.json").read_text())
    rel["program"] = {
        "sha256": sha256(out / "program.json"),
        "bytes": (out / "program.json").stat().st_size,
    }
    rel["weights"] = {
        "sha256": sha256(out / "weights.json"),
        "bytes": (out / "weights.json").stat().st_size,
    }
    (out / "release.json").write_text(json.dumps(rel, separators=(",", ":")), encoding="utf-8")
    for kind in ("program", "weights", "release"):
        p = out / f"{kind}.json"
        print(f"{kind}_sha256={sha256(p)}")
        print(f"{kind}_size={p.stat().st_size}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())

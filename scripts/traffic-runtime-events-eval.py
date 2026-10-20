#!/usr/bin/env python3
"""Evaluate gcloud pubsub pull JSON for the prototype verify-sub.

Does not print message bodies (they may include URLs). Handles the shapes
gcloud --format=json has been seen to emit: base64 data, already-decoded
objects, and Avro JSON string unions.
"""
from __future__ import annotations

import base64
import collections
import json
import sys
from datetime import datetime, timezone


def unwrap_str(v) -> str:
    if v is None:
        return ""
    if isinstance(v, str):
        return v
    if isinstance(v, dict):
        if "string" in v:
            return unwrap_str(v.get("string"))
        if len(v) == 1:
            return unwrap_str(next(iter(v.values())))
    return ""


def try_json_dict(raw) -> dict | None:
    try:
        body = json.loads(raw)
    except Exception:
        return None
    return body if isinstance(body, dict) else None


def decode_b64(s: str):
    compact = "".join(s.split())
    pad = compact + "=" * ((4 - len(compact) % 4) % 4)
    for decoder in (base64.b64decode, base64.urlsafe_b64decode):
        try:
            return decoder(pad, validate=False)
        except Exception:
            continue
    return None


def decode_body(item):
    """Return (body_dict_or_none, skip_reason, attrs)."""
    msg = (item or {}).get("message")
    if not isinstance(msg, dict):
        msg = item if isinstance(item, dict) else {}
    attrs = msg.get("attributes") or {}
    if not isinstance(attrs, dict):
        attrs = {}
    data = msg.get("data")
    if data is None and item is not msg:
        data = (item or {}).get("data")
    if isinstance(data, dict):
        return data, "dict", attrs
    if isinstance(data, list) and data and all(isinstance(x, int) for x in data[:4]):
        try:
            body = try_json_dict(bytes(data))
        except Exception:
            return None, "list_bytes", attrs
        return (body, "list_bytes", attrs) if body else (None, "list_not_json", attrs)
    if isinstance(data, (bytes, bytearray)):
        body = try_json_dict(bytes(data))
        return (body, "bytes", attrs) if body else (None, "bytes_json", attrs)
    if not isinstance(data, str) or not data:
        return None, "empty_t%s" % type(data).__name__, attrs
    body = try_json_dict(data)
    if body is not None:
        return body, "json", attrs
    raw = decode_b64(data)
    if raw is None:
        return None, "b64_fail_len%d" % len(data), attrs
    body = try_json_dict(raw)
    if body is not None:
        return body, "b64", attrs
    prefix = raw[:4].hex() if raw else ""
    kind = "brace" if raw[:1] == b"{" else "bin"
    return None, "b64_not_json_%s_%s" % (kind, prefix), attrs


def params_map(body: dict) -> dict:
    raw_params = body.get("params") or ""
    if isinstance(raw_params, dict):
        return raw_params
    raw_params = unwrap_str(raw_params)
    if not raw_params:
        return {}
    try:
        parsed = json.loads(raw_params)
    except Exception:
        return {}
    return parsed if isinstance(parsed, dict) else {}


def evaluate(items, *, acked: str, now: str | None = None) -> tuple[int, str, str]:
    """Return (exit_code, evidence_text, stdout_line). 0=pass, 1=fail, 2=incomplete."""
    now = now or datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    acked = "yes" if acked == "yes" else "no"
    raw_n = len(items) if isinstance(items, list) else 0
    by_click = collections.defaultdict(set)
    assigned = {}
    names = set()
    attr_names = []
    skip_counts = collections.Counter()
    n = 0
    for item in items or []:
        body, skip, attrs = decode_body(item)
        attr_name = unwrap_str(attrs.get("event_name"))
        if attr_name and attr_name not in attr_names:
            attr_names.append(attr_name)
        name = ""
        click = ""
        eid = ""
        sid = ""
        vid = ""
        if body is not None:
            name = unwrap_str(body.get("event_name")) or attr_name
            click = unwrap_str(body.get("click_id"))
            eid = unwrap_str(body.get("event_id"))
            params = params_map(body)
            sid = unwrap_str(body.get("scenario_id"))
            vid = unwrap_str(body.get("smartlink_variant_id")) or unwrap_str(params.get("variant_id"))
        else:
            skip_counts[skip] += 1
            name = attr_name
            click = unwrap_str(attrs.get("click_id"))
            sid = unwrap_str(attrs.get("scenario_id"))
            vid = unwrap_str(attrs.get("smartlink_variant_id"))
        if not name or not click:
            if body is not None:
                skip_counts["fields"] += 1
            continue
        if eid and eid != f"{click}/{name}":
            evidence = (
                f"utc={now}\nresult=FAIL\nreason=event_id_mismatch event_name={name}\n"
                f"acked_by_gcloud_pull={acked}\n"
            )
            return 1, evidence, ""
        by_click[click].add(name)
        names.add(name)
        n += 1
        if name == "scenario_assigned":
            assigned[click] = {
                "scenario_id": sid,
                "variant_id": vid,
            }
    want = {"ingress", "scenario_assigned", "redirect"}
    ok_click = ""
    seen_sid = []
    seen_vid = []
    for click, have in by_click.items():
        meta = assigned.get(click) or {}
        sid = meta.get("scenario_id") or ""
        vid = meta.get("variant_id") or ""
        if sid and sid not in seen_sid:
            seen_sid.append(sid)
        if vid and vid not in seen_vid:
            seen_vid.append(vid)
        resolved = (
            sid == "redirect"
            and vid == "smartlink-main"
            and not sid.startswith("c1-payload-url")
        )
        if want <= have and resolved:
            ok_click = click
            break
    skip_note = ",".join("%s=%d" % (k, skip_counts[k]) for k in sorted(skip_counts))
    attr_note = ",".join(attr_names[:8])
    if ok_click:
        if acked != "yes":
            evidence = (
                f"utc={now}\nresult=FAIL\nreason=auto_ack_not_used\nacked_by_gcloud_pull={acked}\n"
            )
            return 1, evidence, ""
        evidence = (
            f"utc={now}\n"
            "result=PASS\n"
            f"pulled={n}\n"
            f"pulled_raw={raw_n}\n"
            "triple=ingress,scenario_assigned,redirect\n"
            "same_click_id=yes\n"
            "scenario_id=redirect\n"
            "variant_id=smartlink-main\n"
            "event_id_stable_pattern=click_id/event_name\n"
            f"acked_by_gcloud_pull={acked}\n"
        )
        line = (
            "PASS events triple for one click_id pulled=%d pulled_raw=%d "
            "scenario=redirect variant=smartlink-main auto_ack=%s"
            % (n, raw_n, acked)
        )
        return 0, evidence, line
    reason = (
        "missing_resolved_triple pulled=%d pulled_raw=%d names=%s "
        "scenario_ids=%s variant_ids=%s attr_names=%s skips=%s"
        % (
            n,
            raw_n,
            ",".join(sorted(names)),
            ",".join(seen_sid[:8]),
            ",".join(seen_vid[:8]),
            attr_note,
            skip_note,
        )
    )
    return 2, f"utc={now}\nresult=FAIL\nreason={reason}\nacked_by_gcloud_pull={acked}\n", "events_accum %s" % reason


def main(argv: list[str]) -> int:
    if len(argv) == 2 and argv[1] == "--self-test":
        return self_test()
    if len(argv) != 6:
        sys.stderr.write("usage: traffic-runtime-events-eval.py PULL ACCUM EVIDENCE ACKED FINAL\n")
        return 1
    pull_path, accum_path, evidence_path, acked, final = argv[1:6]
    def load_list(path):
        text = open(path, encoding="utf-8").read().strip()
        raw = json.loads(text) if text else []
        return raw if isinstance(raw, list) else []
    acc = load_list(accum_path)
    acc.extend(load_list(pull_path))
    open(accum_path, "w", encoding="utf-8").write(json.dumps(acc))
    code, evidence, line = evaluate(acc, acked=acked)
    if line:
        print(line)
    if code == 0 or final == "1" or code == 1:
        open(evidence_path, "w", encoding="utf-8").write(evidence)
    return 1 if (code == 2 and final == "1") else code


def self_test() -> int:
    payload = {
        "event_id": "c1/redirect",
        "event_name": "redirect",
        "click_id": "c1",
        "scenario_id": "redirect",
        "smartlink_variant_id": "smartlink-main",
        "params": "{}",
    }
    b64 = base64.b64encode(json.dumps(payload).encode()).decode()
    assigned = dict(payload)
    assigned["event_id"] = "c1/scenario_assigned"
    assigned["event_name"] = "scenario_assigned"
    ingress = dict(payload)
    assigned_b64 = base64.b64encode(json.dumps(assigned).encode()).decode()
    ingress["event_id"] = "c1/ingress"
    ingress["event_name"] = "ingress"
    ingress_b64 = base64.b64encode(json.dumps(ingress).encode()).decode()
    items = [
        {"message": {"data": ingress_b64, "attributes": {"event_name": "ingress"}}},
        {"message": {"data": assigned_b64, "attributes": {"event_name": "scenario_assigned"}}},
        {"message": {"data": payload, "attributes": {"event_name": "redirect"}}},
    ]
    code, evidence, _ = evaluate(items, acked="yes", now="2026-01-01T00:00:00Z")
    if code != 0 or "result=PASS" not in evidence:
        sys.stderr.write("self-test dict-data failed %s %s\n" % (code, evidence))
        return 1
    import tempfile, os
    pull = tempfile.NamedTemporaryFile("w", delete=False)
    accum = tempfile.NamedTemporaryFile("w", delete=False)
    evid = tempfile.NamedTemporaryFile("w", delete=False)
    try:
        json.dump(items, pull)
        pull.close()
        accum.write("[]")
        accum.close()
        evid.close()
        rc = main([sys.argv[0], pull.name, accum.name, evid.name, "yes", "1"])
        if rc != 0:
            sys.stderr.write("self-test argv6 failed rc=%s\n" % rc)
            return 1
    finally:
        os.unlink(pull.name)
        os.unlink(accum.name)
        os.unlink(evid.name)
    avro_items = [
        {"message": {"data": ingress_b64, "attributes": {"event_name": "ingress"}}},
        {"message": {"data": assigned_b64, "attributes": {"event_name": "scenario_assigned"}}},
        {
            "message": {
                "data": json.dumps({"event_name": {"string": "redirect"}, "click_id": {"string": "c1"}, "event_id": {"string": "c1/redirect"}, "scenario_id": {"string": "redirect"}, "smartlink_variant_id": {"string": "smartlink-main"}}),
                "attributes": {"event_name": "redirect"},
            }
        },
    ]
    code, evidence, _ = evaluate(avro_items, acked="yes", now="2026-01-01T00:00:00Z")
    if code != 0:
        sys.stderr.write("self-test avro-json failed %s %s\n" % (code, evidence))
        return 1
    attr_only = [
        {"message": {"data": ingress_b64, "attributes": {"event_name": "ingress", "click_id": "c1"}}},
        {"message": {"data": assigned_b64, "attributes": {"event_name": "scenario_assigned", "click_id": "c1", "scenario_id": "redirect", "smartlink_variant_id": "smartlink-main"}}},
        {
            "message": {
                "data": "!!!!",
                "attributes": {
                    "event_name": "redirect",
                    "click_id": "c1",
                    "scenario_id": "redirect",
                    "smartlink_variant_id": "smartlink-main",
                },
            }
        },
    ]
    code, evidence, _ = evaluate(attr_only, acked="yes", now="2026-01-01T00:00:00Z")
    if code != 0:
        sys.stderr.write("self-test attr-fallback failed %s %s\n" % (code, evidence))
        return 1
    print("self-test ok")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))

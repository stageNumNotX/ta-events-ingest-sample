#!/usr/bin/env python3
"""Evaluate BigQuery JSON as a pass/fail delivery gate.

Per asserted click_id: after event_id dedup, exactly ingress +
scenario_assigned + redirect (no extra/partial chain). Every event's
stored fields must equal the expected strings (not mere presence).
Optional mart file: exactly one row per click_id with the same ids.
Empty results, missing clicks, mismatches, and extra events fail closed.
Does not print row payloads.
"""
from __future__ import annotations

import json
import os
import re
import sys
import tempfile
from collections import defaultdict
from io import StringIO

REQUIRED_EVENTS = ("ingress", "redirect", "scenario_assigned")
CLICK_ID_RE = re.compile(r"^[A-Za-z0-9._:-]{1,128}$")


def _as_str(v) -> str:
    if v is None:
        return ""
    return str(v)


def _mart_found(row: dict) -> bool:
    flag = row.get("mart_found")
    if flag is True:
        return True
    if flag is False or flag is None:
        return False
    return str(flag).strip().lower() in ("true", "1", "t")


def parse_click_ids(raw: str) -> list[str]:
    ids = [x.strip() for x in raw.split(",") if x.strip()]
    if not ids:
        raise ValueError("T11_CLICK_IDS empty")
    seen = set()
    out = []
    for item in ids:
        if not CLICK_ID_RE.fullmatch(item):
            raise ValueError("T11_CLICK_IDS contains invalid click_id")
        if item in seen:
            raise ValueError("T11_CLICK_IDS contains duplicate click_id")
        seen.add(item)
        out.append(item)
    return out


def sql_in_list(raw: str) -> str:
    ids = parse_click_ids(raw)
    return ",".join("'" + item.replace("'", "''") + "'" for item in ids)


def _real_events(rows: list) -> list[dict]:
    real = []
    for row in rows:
        if _as_str(row.get("event_name")):
            real.append(row)
    return real


def _dedup_event_id(rows: list) -> list[dict]:
    seen = set()
    out = []
    for row in rows:
        eid = _as_str(row.get("event_id"))
        if eid:
            if eid in seen:
                continue
            seen.add(eid)
        out.append(row)
    return out


def evaluate(
    rows,
    click_ids,
    expect_source,
    expect_sub,
    expect_sha,
    expect_campaign=None,
    expect_zone=None,
    expect_country=None,
    mart_rows=None,
    expect_revision=None,
    require_mart=False,
    final=False,
) -> tuple[int, str]:
    if final:
        require_mart = True
        if expect_country is None or expect_country == "":
            return 1, "result=FAIL\nreason=final_country_required\n"
        if expect_revision is None or expect_revision == "":
            return 1, "result=FAIL\nreason=final_k_revision_required\n"
    if not click_ids:
        return 1, "result=FAIL\nreason=no_click_ids\n"
    rows = _dedup_event_id(list(rows or []))
    by_click: dict[str, list] = defaultdict(list)
    for row in rows:
        if not isinstance(row, dict):
            return 1, "result=FAIL\nreason=row_not_object\n"
        cid = _as_str(row.get("click_id"))
        if not cid:
            return 1, "result=FAIL\nreason=row_missing_click_id\n"
        by_click[cid].append(row)

    failures = []
    if not rows:
        failures.append("empty_result")
    for cid in click_ids:
        real = _real_events(by_click.get(cid, []))
        if cid not in by_click:
            failures.append("missing_click_id=%s" % cid)
            continue
        names = [_as_str(ev.get("event_name")) for ev in real]
        want = sorted(REQUIRED_EVENTS)
        got = sorted(names)
        if got != want:
            if set(want) - set(names) or len(names) < 3:
                failures.append(
                    "incomplete_events click_id=%s names=%s"
                    % (cid, ",".join(got))
                )
            if len(names) > 3 or set(names) - set(want) or len(names) != len(set(names)):
                failures.append(
                    "extra_events click_id=%s names=%s"
                    % (cid, ",".join(got))
                )
        for ev in real:
            name = _as_str(ev.get("event_name"))
            eid = _as_str(ev.get("event_id"))
            if eid != "%s/%s" % (cid, name):
                failures.append("event_id_mismatch click_id=%s" % cid)
                break
        if not real:
            continue
        if any(_as_str(ev.get("ta_source")) != expect_source for ev in real):
            failures.append("ta_source_mismatch click_id=%s" % cid)
        if any(_as_str(ev.get("ta_sub")) != expect_sub for ev in real):
            failures.append("ta_sub_mismatch click_id=%s" % cid)
        if any(_as_str(ev.get("contract_release_sha256")) != expect_sha for ev in real):
            failures.append("release_sha_mismatch click_id=%s" % cid)
        if expect_campaign is not None:
            if any(_as_str(ev.get("campaign_id")) != expect_campaign for ev in real):
                failures.append("campaign_id_mismatch click_id=%s" % cid)
        if expect_zone is not None:
            if any(_as_str(ev.get("zone_id")) != expect_zone for ev in real):
                failures.append("zone_id_mismatch click_id=%s" % cid)
        if expect_country is not None:
            if any(_as_str(ev.get("country")) != expect_country for ev in real):
                failures.append("country_mismatch click_id=%s" % cid)
        if expect_revision is not None:
            if any(_as_str(ev.get("k_revision")) != expect_revision for ev in real):
                failures.append("k_revision_mismatch click_id=%s" % cid)

    extra = sorted(set(by_click) - set(click_ids))
    for cid in extra:
        failures.append("unexpected_click_id=%s" % cid)

    if require_mart and mart_rows is None:
        failures.append("mart_required")
    elif mart_rows is not None:
        if not isinstance(mart_rows, list):
            failures.append("mart_not_list")
        else:
            mart_by = {}
            for row in mart_rows:
                if not isinstance(row, dict):
                    failures.append("mart_row_not_object")
                    continue
                requested = _as_str(row.get("requested_click_id"))
                found_cid = _as_str(row.get("click_id"))
                key = requested or found_cid
                if not key:
                    failures.append("mart_missing_click_id")
                    continue
                if key in mart_by:
                    failures.append("mart_duplicate_click_id=%s" % key)
                    continue
                mart_by[key] = row
            for cid in click_ids:
                row = mart_by.get(cid)
                if row is None:
                    failures.append("mart_missing_click_id=%s" % cid)
                    continue
                found_cid = _as_str(row.get("click_id"))
                if not _mart_found(row) or found_cid != cid:
                    failures.append("mart_missing_click_id=%s" % cid)
                    continue
                if _as_str(row.get("ta_source")) != expect_source:
                    failures.append("mart_ta_source_mismatch click_id=%s" % cid)
                if _as_str(row.get("ta_sub")) != expect_sub:
                    failures.append("mart_ta_sub_mismatch click_id=%s" % cid)
                if expect_campaign is not None and _as_str(row.get("campaign_id")) != expect_campaign:
                    failures.append("mart_campaign_id_mismatch click_id=%s" % cid)
                if expect_zone is not None and _as_str(row.get("zone_id")) != expect_zone:
                    failures.append("mart_zone_id_mismatch click_id=%s" % cid)
                if expect_country is not None and _as_str(row.get("country")) != expect_country:
                    failures.append("mart_country_mismatch click_id=%s" % cid)
            for cid in sorted(set(mart_by) - set(click_ids)):
                failures.append("mart_unexpected_click_id=%s" % cid)

    if failures:
        seen = set()
        lines = ["result=FAIL"]
        for item in failures:
            if item in seen:
                continue
            seen.add(item)
            lines.append("reason=" + item)
        return 1, "\n".join(lines) + "\n"
    return 0, (
        "result=PASS\n"
        "clicks=%d\n"
        "triple=ingress,scenario_assigned,redirect\n"
        "source_sub_sha=exact_match\n" % len(click_ids)
    )


def _event(
    click,
    name,
    source="network-a",
    sub="campaign-42",
    sha="abc123",
    campaign="",
    zone="",
    country="",
    revision="rev-1",
):
    return {
        "click_id": click,
        "event_id": "%s/%s" % (click, name),
        "event_name": name,
        "ta_source": source,
        "ta_sub": sub,
        "campaign_id": campaign,
        "zone_id": zone,
        "country": country,
        "contract_release_sha256": sha,
        "k_revision": revision,
    }


def _triple(
    click="c1",
    source="network-a",
    sub="campaign-42",
    sha="abc123",
    campaign="",
    zone="",
    country="",
    revision="rev-1",
):
    return [
        _event(click, name, source, sub, sha, campaign, zone, country, revision)
        for name in REQUIRED_EVENTS
    ]


def _mart(click="c1", source="network-a", sub="campaign-42", campaign="", zone="", country="", found=True):
    return {
        "requested_click_id": click,
        "click_id": click if found else None,
        "ta_source": source if found else None,
        "ta_sub": sub if found else None,
        "campaign_id": campaign if found else None,
        "zone_id": zone if found else None,
        "country": country if found else None,
        "mart_found": found,
    }


def self_test() -> int:
    click = ["c1"]
    sha = "abc123"
    code, evidence = evaluate(_triple(), click, "network-a", "campaign-42", sha)
    if code != 0 or "result=PASS" not in evidence or "exact_match" not in evidence:
        sys.stderr.write("self-test full failed %s %s\n" % (code, evidence))
        return 1

    code, evidence = evaluate([], click, "network-a", "campaign-42", sha)
    if code == 0 or "empty_result" not in evidence or "missing_click_id=c1" not in evidence:
        sys.stderr.write("self-test empty failed %s %s\n" % (code, evidence))
        return 1

    partial = [_event("c1", "ingress")]
    code, evidence = evaluate(partial, click, "network-a", "campaign-42", sha)
    if code == 0 or "incomplete_events" not in evidence:
        sys.stderr.write("self-test partial failed %s %s\n" % (code, evidence))
        return 1

    left_join_empty = [{"click_id": "c1", "event_id": None, "event_name": None}]
    code, evidence = evaluate(left_join_empty, click, "network-a", "campaign-42", sha)
    if code == 0 or "incomplete_events" not in evidence:
        sys.stderr.write("self-test left-join empty failed %s %s\n" % (code, evidence))
        return 1

    two = ["c1", "c2"]
    code, evidence = evaluate(_triple(), two, "network-a", "campaign-42", sha)
    if code == 0 or "missing_click_id=c2" not in evidence:
        sys.stderr.write("self-test missing second click failed %s %s\n" % (code, evidence))
        return 1

    wrong = _triple(source="other", sha="nope")
    code, evidence = evaluate(wrong, click, "network-a", "campaign-42", sha)
    if code == 0 or "ta_source_mismatch" not in evidence or "release_sha_mismatch" not in evidence:
        sys.stderr.write("self-test mismatch failed %s %s\n" % (code, evidence))
        return 1

    mixed = _triple()
    mixed[0] = _event("c1", "ingress", source="other")
    code, evidence = evaluate(mixed, click, "network-a", "campaign-42", sha)
    if code == 0 or "ta_source_mismatch" not in evidence:
        sys.stderr.write("self-test mixed-per-event failed %s %s\n" % (code, evidence))
        return 1

    extra = _triple() + [_event("c1", "pageview")]
    code, evidence = evaluate(extra, click, "network-a", "campaign-42", sha)
    if code == 0 or "extra_events" not in evidence:
        sys.stderr.write("self-test extra event failed %s %s\n" % (code, evidence))
        return 1

    dup_ingress = _triple() + [
        {
            "click_id": "c1",
            "event_id": "c1/ingress-dup",
            "event_name": "ingress",
            "ta_source": "network-a",
            "ta_sub": "campaign-42",
            "contract_release_sha256": sha,
        }
    ]
    code, evidence = evaluate(dup_ingress, click, "network-a", "campaign-42", sha)
    if code == 0 or "extra_events" not in evidence:
        sys.stderr.write("self-test extra chain failed %s %s\n" % (code, evidence))
        return 1

    same_id_dup = _triple() + [_event("c1", "ingress")]
    code, evidence = evaluate(same_id_dup, click, "network-a", "campaign-42", sha)
    if code != 0:
        sys.stderr.write("self-test event_id dedup failed %s %s\n" % (code, evidence))
        return 1

    presence_only = [
        {
            "click_id": "c1",
            "event_names": ["ingress", "redirect", "scenario_assigned"],
            "ta_source_present": 3,
            "ta_sub_present": 3,
            "provenance_present": 3,
        }
    ]
    code, evidence = evaluate(presence_only, click, "network-a", "campaign-42", sha)
    if code == 0 or "incomplete_events" not in evidence:
        sys.stderr.write("self-test presence-only failed %s %s\n" % (code, evidence))
        return 1

    nonempty_wrong = _triple(source="present-but-wrong")
    code, evidence = evaluate(nonempty_wrong, click, "network-a", "campaign-42", sha)
    if code == 0 or "ta_source_mismatch" not in evidence:
        sys.stderr.write("self-test nonempty-wrong failed %s %s\n" % (code, evidence))
        return 1

    attrs = _triple(campaign="123", zone="987", country="DE")
    code, evidence = evaluate(
        attrs, click, "network-a", "campaign-42", sha, "123", "987", "DE", [_mart(campaign="123", zone="987", country="DE")]
    )
    if code != 0:
        sys.stderr.write("self-test attr mart pass failed %s %s\n" % (code, evidence))
        return 1

    code, evidence = evaluate(
        attrs, click, "network-a", "campaign-42", sha, "123", "987", "DE", []
    )
    if code == 0 or "mart_missing_click_id=c1" not in evidence:
        sys.stderr.write("self-test mart missing failed %s %s\n" % (code, evidence))
        return 1

    mixed_camp = _triple(campaign="123", zone="987", country="DE")
    mixed_camp[0] = _event("c1", "ingress", campaign="999", zone="987", country="DE")
    code, evidence = evaluate(
        mixed_camp, click, "network-a", "campaign-42", sha, "123", "987", "DE"
    )
    if code == 0 or "campaign_id_mismatch" not in evidence:
        sys.stderr.write("self-test mixed campaign failed %s %s\n" % (code, evidence))
        return 1

    left_join_mart = [_mart(found=False)]
    code, evidence = evaluate(
        _triple(), click, "network-a", "campaign-42", sha, "", "", "", left_join_mart
    )
    if code == 0 or "mart_missing_click_id=c1" not in evidence:
        sys.stderr.write("self-test left-join mart missing failed %s %s\n" % (code, evidence))
        return 1

    legacy_using = [{"click_id": "c1", "ta_source": None, "ta_sub": None, "campaign_id": None, "zone_id": None, "country": None}]
    code, evidence = evaluate(
        _triple(), click, "network-a", "campaign-42", sha, "", "", "", legacy_using
    )
    if code == 0 or "mart_missing_click_id=c1" not in evidence:
        sys.stderr.write("self-test legacy USING click_id failed %s %s\n" % (code, evidence))
        return 1

    wrong_sub = [_mart(sub="WRONG_SUB")]
    code, evidence = evaluate(
        _triple(), click, "network-a", "campaign-42", sha, "", "", "", wrong_sub
    )
    if code == 0 or "mart_ta_sub_mismatch click_id=c1" not in evidence:
        sys.stderr.write("self-test mart wrong ta_sub failed %s %s\n" % (code, evidence))
        return 1

    code, evidence = evaluate(
        _triple(), click, "network-a", "campaign-42", sha, require_mart=True
    )
    if code == 0 or "mart_required" not in evidence:
        sys.stderr.write("self-test mart required failed %s %s\n" % (code, evidence))
        return 1

    old_rev = _triple(revision="old-rev", country="RU")
    code, evidence = evaluate(
        old_rev, click, "network-a", "campaign-42", sha, "", "", "RU",
        [_mart(country="RU")], expect_revision="new-rev", require_mart=True, final=True,
    )
    if code == 0 or "k_revision_mismatch" not in evidence:
        sys.stderr.write("self-test old revision failed %s %s\n" % (code, evidence))
        return 1

    empty_rev_events = _triple(revision="", country="RU")
    code, evidence = evaluate(
        empty_rev_events, click, "network-a", "campaign-42", sha, "", "", "RU",
        [_mart(country="RU")], expect_revision="", require_mart=True, final=True,
    )
    if code == 0 or "final_k_revision_required" not in evidence:
        sys.stderr.write("self-test empty expected revision failed %s %s\n" % (code, evidence))
        return 1

    code, evidence = evaluate(
        _triple(country=""), click, "network-a", "campaign-42", sha, "", "", "",
        [_mart(country="")], expect_revision="rev-1", require_mart=True, final=True,
    )
    if code == 0 or "final_country_required" not in evidence:
        sys.stderr.write("self-test empty expected country failed %s %s\n" % (code, evidence))
        return 1

    code, evidence = evaluate(
        _triple(revision="rev-1", country="RU"), click, "network-a", "campaign-42", sha, "", "", "RU",
        [_mart(country="RU")], expect_revision="rev-1", require_mart=True, final=True,
    )
    if code != 0:
        sys.stderr.write("self-test final pass failed %s %s\n" % (code, evidence))
        return 1

    def _tmp(obj):
        fh = tempfile.NamedTemporaryFile("w", suffix=".json", delete=False)
        json.dump(obj, fh)
        fh.close()
        return fh.name

    rows_empty = _tmp(_triple(revision="", country=""))
    mart_empty = _tmp([_mart(country="")])
    rows_ok = _tmp(_triple(revision="rev-1", country="RU"))
    mart_ok = _tmp([_mart(country="RU")])
    try:
        buf = StringIO()
        orig = sys.stdout
        sys.stdout = buf
        try:
            code = main(["eval", "--final", rows_empty, mart_empty, "c1", "network-a", "campaign-42", "", "", "", "abc123", "rev-1"])
        finally:
            sys.stdout = orig
        if code == 0 or "final_country_required" not in buf.getvalue():
            sys.stderr.write("self-test --final empty country failed %s %s\n" % (code, buf.getvalue()))
            return 1
        buf = StringIO()
        sys.stdout = buf
        try:
            code = main(["eval", "--final", rows_ok, mart_ok, "c1", "network-a", "campaign-42", "", "", "RU", "abc123", ""])
        finally:
            sys.stdout = orig
        if code == 0 or "final_k_revision_required" not in buf.getvalue():
            sys.stderr.write("self-test --final empty revision failed %s %s\n" % (code, buf.getvalue()))
            return 1
        buf = StringIO()
        sys.stdout = buf
        try:
            code = main(["eval", "--final", rows_ok, mart_ok, "c1", "network-a", "campaign-42", "", "", "RU", "abc123", "rev-1"])
        finally:
            sys.stdout = orig
        if code != 0 or "result=PASS" not in buf.getvalue():
            sys.stderr.write("self-test --final positive failed %s %s\n" % (code, buf.getvalue()))
            return 1
    finally:
        for p in (rows_empty, mart_empty, rows_ok, mart_ok):
            try:
                os.unlink(p)
            except OSError:
                pass

    if sql_in_list("c1,c2") != "'c1','c2'":
        sys.stderr.write("self-test sql-in failed\n")
        return 1
    try:
        parse_click_ids("")
        sys.stderr.write("self-test empty click ids should fail\n")
        return 1
    except ValueError:
        pass

    print("self-test ok")
    return 0


def _load_json_list(path: str):
    raw = open(path, encoding="utf-8").read().strip()
    if not raw:
        return [], None
    try:
        rows = json.loads(raw)
    except json.JSONDecodeError:
        return None, "invalid_json"
    if rows is None:
        rows = []
    if not isinstance(rows, list):
        return None, "rows_not_list"
    return rows, None


def main(argv: list[str]) -> int:
    if len(argv) == 2 and argv[1] == "--self-test":
        return self_test()
    if len(argv) == 3 and argv[1] == "--sql-in":
        try:
            sys.stdout.write(sql_in_list(argv[2]) + "\n")
        except ValueError as exc:
            sys.stderr.write("%s\n" % exc)
            return 1
        return 0
    if len(argv) >= 2 and argv[1] in ("--attr", "--final"):
        final = argv[1] == "--final"
        need = 12 if final else 11
        if len(argv) != need:
            sys.stderr.write(
                "usage: traffic-runtime-t11-eval.py --attr ROWS.json MART.json CLICK_IDS "
                "EXPECT_SOURCE EXPECT_SUB EXPECT_CAMPAIGN EXPECT_ZONE EXPECT_COUNTRY EXPECT_SHA\n"
                "       traffic-runtime-t11-eval.py --final ... EXPECT_SHA EXPECT_K_REVISION\n"
                "MART.json may be - to skip the mart check only with --attr (probe)\n"
            )
            return 1
        rows, err = _load_json_list(argv[2])
        if err:
            sys.stderr.write("result=FAIL\nreason=%s\n" % err)
            return 1
        mart = None
        if argv[3] != "-":
            mart, err = _load_json_list(argv[3])
            if err:
                sys.stderr.write("result=FAIL\nreason=mart_%s\n" % err)
                return 1
        elif final:
            sys.stderr.write("result=FAIL\nreason=mart_required\n")
            return 1
        try:
            click_ids = parse_click_ids(argv[4])
        except ValueError as exc:
            sys.stderr.write("result=FAIL\nreason=%s\n" % exc)
            return 1
        expect_revision = argv[11] if final else None
        if final and argv[9] == "":
            sys.stdout.write("result=FAIL\nreason=final_country_required\n")
            return 1
        if final and argv[11] == "":
            sys.stdout.write("result=FAIL\nreason=final_k_revision_required\n")
            return 1
        code, evidence = evaluate(
            rows,
            click_ids,
            argv[5],
            argv[6],
            argv[10],
            expect_campaign=argv[7],
            expect_zone=argv[8],
            expect_country=argv[9],
            mart_rows=mart,
            expect_revision=expect_revision,
            require_mart=final,
            final=final,
        )
        sys.stdout.write(evidence)
        return code
    if len(argv) != 6:
        sys.stderr.write(
            "usage: traffic-runtime-t11-eval.py ROWS.json CLICK_IDS EXPECT_SOURCE EXPECT_SUB EXPECT_SHA\n"
        )
        return 1
    rows, err = _load_json_list(argv[1])
    if err:
        sys.stderr.write("result=FAIL\nreason=%s\n" % err)
        return 1
    try:
        click_ids = parse_click_ids(argv[2])
    except ValueError as exc:
        sys.stderr.write("result=FAIL\nreason=%s\n" % exc)
        return 1
    code, evidence = evaluate(rows, click_ids, argv[3], argv[4], argv[5])
    sys.stdout.write(evidence)
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv))

#!/usr/bin/env python3
"""Read-only capability probe for PR #10; stdout/files contain projections only.

Uses the existing gcloud identity. No publish/pull/ack, BQ queries, deployments,
IAM writes, service enablement, secret access, or environment dumps. Metadata
access does not prove schema compatibility, effective publish permission, or live
delivery. Keep this scoped collector separate from production deploy scripts.
"""

import argparse
import hashlib
import json
import os
import re
import subprocess
from pathlib import Path


PROJECT = os.environ.get("PROJECT_ID", "example-gcp-project")
PROJECT_NUMBER = os.environ.get("GCP_PROJECT_NUMBER", "123456789012")
REGION = "europe-west1"
TOPICS = ("ta-events", "ta-traffic-runtime-proto-events")
SERVICES = ("ta-events-ingest", "ta-traffic-runtime-proto", "ta-traffic-runtime-proto-probe")
ENV_KEYS = {
    "PUBSUB_TOPIC", "TRAFFIC_CONTEXT_MODE", "PROTOTYPE_MODE", "ARTIFACT_SOURCE",
    "TRAFFIC_USE_POINTER", "TRAFFIC_POINTER_POLL", "TRAFFIC_PIN_RELEASE_SHA256",
    "TRAFFIC_PIN_RELEASE_GENERATION", "TRAFFIC_POINTER_SHA256", "TRAFFIC_POINTER_GENERATION",
}


def compact(value):
    # Escape Markdown/HTML delimiters too: retrieved metadata is data, not instructions.
    return json.dumps(value, ensure_ascii=True, sort_keys=True, separators=(",", ":")).replace("`", "\\u0060").replace("<", "\\u003c").replace(">", "\\u003e")


def select(value, keys):
    return {key: value[key] for key in keys if key in value}


def scoped(name, kind):
    return bool(re.fullmatch(rf"projects/({PROJECT}|{PROJECT_NUMBER})/{kind}/[A-Za-z][A-Za-z0-9._~+%-]*", name))


def topic_id(value):
    """Accept a topic only from selected env on the known task revisions."""
    if scoped(value, "topics"):
        return value.rsplit("/", 1)[-1]
    if re.fullmatch(r"[A-Za-z][A-Za-z0-9._~+%-]*", value):
        return value
    raise ValueError("topic outside project")


def error_kind(error):
    upper = error.upper()
    for key, terms in {
        "PERMISSION_DENIED": ("PERMISSION_DENIED", "PERMISSION DENIED", "DOES NOT HAVE", "ACCESS DENIED", "FORBIDDEN"),
        "NOT_FOUND": ("NOT_FOUND", "NOT FOUND", "DOES NOT EXIST"),
        "UNAUTHENTICATED": ("UNAUTHENTICATED", "INVALID_GRANT", "REAUTH", "NO ACTIVE ACCOUNT"),
        "API_DISABLED": ("SERVICE_DISABLED", "HAS NOT BEEN USED", "IS DISABLED"),
    }.items():
        if any(term in upper for term in terms):
            return key
    return "COMMAND_FAILED"


class Inventory:
    def __init__(self, runner=subprocess.run):
        self.runner = runner
        self.lines = []
        self.failed = False

    def record(self, label, data):
        self.lines.append(f"- **{label}**: `{compact(data)}`")

    def fail(self, label, reason):
        self.failed = True
        self.record(label, {"read": reason})

    def read(self, label, args):
        try:
            result = self.runner(args, capture_output=True, text=True, timeout=20, check=False)
            if result.returncode:
                self.fail(label, error_kind(result.stderr + "\n" + result.stdout))
                return None
            return json.loads(result.stdout)
        except subprocess.TimeoutExpired:
            self.fail(label, "TIMEOUT")
        except (OSError, ValueError):
            self.fail(label, "TOOL_OR_JSON_ERROR")
        return None

    def gcloud(self, label, *args):
        return self.read(label, ["gcloud", *args, f"--project={PROJECT}", "--format=json", "--quiet"])

    def run(self):
        principals = {f"serviceAccount:github-actions-sa@{PROJECT}.iam.gserviceaccount.com"}
        topics = set(TOPICS)
        for service in SERVICES:
            data = self.gcloud(f"run/{service}", "run", "services", "describe", service, f"--region={REGION}")
            if data is None:
                continue
            status = data.get("status", {})
            traffic = [select(t, ("revisionName", "percent", "tag", "latestRevision")) for t in status.get("traffic", [])]
            self.record(f"run/{service}", {"traffic": traffic, **select(status, ("latestReadyRevisionName", "latestCreatedRevisionName"))})
            revisions = {t.get("revisionName") for t in traffic}
            revisions.update(status.get(k) for k in ("latestReadyRevisionName", "latestCreatedRevisionName"))
            for revision in sorted(r for r in revisions if r):
                if not re.fullmatch(re.escape(service) + r"-[a-z0-9-]+", revision):
                    self.fail(f"run/{service}", "REVISION_OUTSIDE_SCOPE")
                    continue
                rev = self.gcloud(f"revision/{revision}", "run", "revisions", "describe", revision, f"--region={REGION}")
                if rev is None:
                    continue
                spec = rev.get("spec", {})
                account = spec.get("serviceAccountName", "UNKNOWN")
                if account.endswith(f"@{PROJECT}.iam.gserviceaccount.com") or account == f"{PROJECT_NUMBER}-compute@developer.gserviceaccount.com":
                    principals.add(f"serviceAccount:{account}")
                containers = []
                for container in spec.get("containers", []):
                    env = {e["name"]: e.get("value", "SECRET_REF_NOT_READ") for e in container.get("env", []) if e.get("name") in ENV_KEYS}
                    containers.append({"image": container.get("image"), "selected_env": env})
                    configured_topic = env.get("PUBSUB_TOPIC")
                    if configured_topic:
                        try:
                            if configured_topic == "SECRET_REF_NOT_READ":
                                raise ValueError("topic is a secret reference")
                            topics.add(topic_id(configured_topic))
                        except ValueError:
                            self.fail(f"revision/{revision}/PUBSUB_TOPIC", "UNRESOLVED_OR_OUTSIDE_PROJECT_NOT_READ")
                labels = select(rev.get("metadata", {}).get("labels", {}), ("commit-sha", "managed-by"))
                self.record(f"revision/{revision}", {"service_account": account, "containers": containers, "image_digest": rev.get("status", {}).get("imageDigest", "UNKNOWN"), "code_labels": labels, "active_release": "NOT_CHECKED"})

        project_policy = self.gcloud("publisher IAM/project", "projects", "get-iam-policy", PROJECT)
        if project_policy is not None:
            self.record("publisher IAM/project", self.bindings(project_policy, principals))
        tables = {"events_raw.events", "events_staging.events", "events_marts.click_funnel", "traffic.events_raw", "traffic.clicks"}
        seen_schemas = set()
        for topic in sorted(topics):
            data = self.gcloud(f"topic/{topic}", "pubsub", "topics", "describe", topic)
            if data is None:
                continue
            self.record(f"topic/{topic}", select(data, ("name", "schemaSettings", "messageRetentionDuration", "state")))
            policy = self.gcloud(f"publisher IAM/{topic}", "pubsub", "topics", "get-iam-policy", topic)
            if policy is not None:
                self.record(f"publisher IAM/{topic}", self.bindings(policy, principals))
            schema = data.get("schemaSettings", {}).get("schema")
            if schema and schema not in seen_schemas:
                seen_schemas.add(schema)
                if not scoped(schema, "schemas"):
                    self.fail("linked schema", "OUTSIDE_PROJECT_NOT_READ")
                else:
                    revisions = self.gcloud(f"schema/{schema}", "pubsub", "schemas", "list-revisions", schema, "--view=full")
                    if revisions is not None:
                        for revision in sorted(revisions, key=lambda r: r.get("revisionId", "")):
                            try:
                                self.record("schema revision", self.schema_summary(revision))
                            except (ValueError, AttributeError, KeyError):
                                self.fail(f"schema/{schema}", "AVRO_DEFINITION_NOT_READ")
            subs = self.gcloud(f"subscriptions/{topic}", "pubsub", "topics", "list-subscriptions", topic)
            if subs is None:
                continue
            names = [s if isinstance(s, str) else s.get("name", "") for s in subs]
            self.record(f"subscriptions/{topic}", {"count": len(names)})
            for sub in sorted(names):
                if not scoped(sub, "subscriptions"):
                    self.fail("linked subscription", "OUTSIDE_PROJECT_NOT_READ")
                    continue
                detail = self.gcloud(f"subscription/{sub}", "pubsub", "subscriptions", "describe", sub)
                if detail is None:
                    continue
                summary = select(detail, ("name", "topic", "state", "ackDeadlineSeconds", "deadLetterPolicy", "messageRetentionDuration", "enableExactlyOnceDelivery", "enableMessageOrdering"))
                summary["bigqueryConfig"] = select(detail.get("bigqueryConfig", {}), ("table", "useTopicSchema", "useTableSchema", "writeMetadata", "dropUnknownFields", "state", "serviceAccountEmail"))
                summary["push_configured"] = bool(detail.get("pushConfig", {}).get("pushEndpoint"))
                self.record(f"subscription/{sub}", summary)
                table = summary["bigqueryConfig"].get("table", "")
                match = re.fullmatch(re.escape(PROJECT) + r"[.:]([A-Za-z_][A-Za-z0-9_]*\.[A-Za-z_][A-Za-z0-9_]*)", table)
                if match:
                    tables.add(match[1])
                elif table:
                    self.fail("linked BQ table", "OUTSIDE_PROJECT_NOT_READ")
        for table in sorted(tables):
            data = self.read(f"BQ/{table}", ["bq", "--format=json", f"--project_id={PROJECT}", "show", f"{PROJECT}:{table}"])
            if data is not None:
                fields = data.get("schema", {}).get("fields", [])
                columns = {f["name"]: f.get("type", "UNKNOWN") + "/" + f.get("mode", "NULLABLE") for f in fields}
                view = data.get("view", {}).get("query")
                self.record(f"BQ/{table}", {**select(data, ("type", "location", "timePartitioning", "clustering", "requirePartitionFilter")), "columns": columns, "view_sql_sha256": hashlib.sha256(view.encode()).hexdigest() if view else None})
        self.record("publisher IAM interpretation", "Only matching direct/project bindings; inheritance, custom roles, groups, conditions and data-plane ACK are not evaluated. No binding is not proof of denial.")
        return {"status": "PARTIAL" if self.failed else "METADATA_READ_COMPLETE", "report": "\n\n".join(self.lines)}

    @staticmethod
    def bindings(policy, principals):
        rows = []
        for binding in policy.get("bindings", []):
            matched = sorted(set(binding.get("members", [])) & (principals | {"allUsers", "allAuthenticatedUsers"}))
            if matched:
                rows.append({"role": binding.get("role"), "members": matched, "conditional": bool(binding.get("condition"))})
        return sorted(rows, key=compact)

    @staticmethod
    def schema_summary(revision):
        definition = revision.get("definition", "")
        # Exact field types/defaults from the FULL schema; no message data/doc strings.
        fields = json.loads(definition)["fields"]
        projected = {field["name"]: select(field, ("type", "default")) for field in fields}
        return {**select(revision, ("name", "type", "revisionId", "revisionCreateTime")), "definition_sha256": hashlib.sha256(definition.encode()).hexdigest(), "fields": projected}


def self_test():
    # Security boundary: unselected values, stderr and other IAM members must
    # never leak into the report. Also exercise PARTIAL instead of false success.
    secret = "DO_NOT_EXPOSE_9a87"
    calls = []

    def fake(args, **kwargs):
        calls.append(args)
        if "services" in args:
            data = {"status": {"traffic": [{"revisionName": args[4] + "-00001-a", "percent": 100}], "secret": secret}}
        elif "revisions" in args:
            data = {"spec": {"containers": [{"image": "image@sha256:123", "env": [{"name": "PUBSUB_TOPIC", "value": "traffic-runtime-t11-events"}, {"name": "EVENTS_INGEST_SECRET", "value": secret}]}]}}
        elif "get-iam-policy" in args:
            data = {"bindings": [{"role": "roles/owner", "members": ["user:" + secret]}]}
        else:
            return subprocess.CompletedProcess(args, 1, "", "PERMISSION_DENIED " + secret)
        return subprocess.CompletedProcess(args, 0, json.dumps(data), "")

    result = Inventory(fake).run()
    assert result["status"] == "PARTIAL" and "PERMISSION_DENIED" in result["report"]
    assert secret not in compact(result) and "EVENTS_INGEST_SECRET" not in result["report"]
    assert "ta-events" in result["report"] and "NOT_CHECKED" in result["report"]
    forbidden = {"publish", "pull", "ack", "deploy", "delete", "create", "update", "add-iam-policy-binding", "query"}
    assert not any(forbidden.intersection(call) for call in calls)
    assert not scoped("projects/other/subscriptions/x", "subscriptions")
    assert scoped(f"projects/{PROJECT_NUMBER}/schemas/schema-v1", "schemas")
    assert topic_id(f"projects/{PROJECT_NUMBER}/topics/traffic-runtime-t11-events") == "traffic-runtime-t11-events"
    try:
        topic_id("projects/other/topics/traffic-runtime-t11-events")
    except ValueError:
        pass
    else:
        raise AssertionError("cross-project topic accepted")
    assert not scoped(f"projects/{PROJECT}/subscriptions/x/../../secrets", "subscriptions")
    assert "`" not in compact("` @cursor <script>")
    full = Inventory.schema_summary({"revisionId": "abc", "definition": '{"fields":[{"name":"zone_id","type":"string","default":""}]}'})
    assert full["fields"]["zone_id"] == {"type": "string", "default": ""}
    try:
        Inventory.schema_summary({"revisionId": "abc"})
    except ValueError:
        pass
    else:
        raise AssertionError("missing FULL schema definition accepted")

    def complete(args, **kwargs):
        if args[0] == "bq":
            data = {"type": "TABLE", "schema": {"fields": [{"name": "zone_id", "type": "STRING"}]}}
        elif "list-subscriptions" in args:
            data = [{"name": f"projects/{PROJECT_NUMBER}/subscriptions/ta-events-to-bq"}]
        elif "subscriptions" in args:
            data = {"bigqueryConfig": {"table": PROJECT + ".events_raw.events", "useTopicSchema": True}}
        elif "list-revisions" in args:
            data = [{"revisionId": "abc", "definition": '{"fields":[{"name":"zone_id","type":"string","default":""}]}'}]
        elif "topics" in args and "describe" in args:
            data = {"schemaSettings": {"schema": f"projects/{PROJECT_NUMBER}/schemas/ta-events-schema"}}
        else:
            return fake(args, **kwargs)
        return subprocess.CompletedProcess(args, 0, json.dumps(data), "")

    result = Inventory(complete).run()
    assert result["status"] == "METADATA_READ_COMPLETE"
    assert "STRING/NULLABLE" in result["report"] and '"revisionId":"abc"' in result["report"]
    assert "topic/traffic-runtime-t11-events" in result["report"]
    assert secret not in compact(result)
    print("inventory self-test: PASS (sanitization, scope, read-only commands, partial failures)")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--self-test", action="store_true")
    parser.add_argument("--output", type=Path)
    options = parser.parse_args()
    if options.self_test:
        self_test()
    else:
        result = Inventory().run()
        if options.output:
            options.output.write_text(json.dumps(result, ensure_ascii=False), encoding="utf-8")
        print(result["status"] + "\n\n" + result["report"])
        raise SystemExit(0 if result["status"] == "METADATA_READ_COMPLETE" else 1)

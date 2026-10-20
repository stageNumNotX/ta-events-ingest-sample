# Q-MACRO-CONTRACT digest package

Producer (`stageNumNotX/compiler-service`) is the **sole oracle**. Pin **`oracleDigest`**, not a git SHA.

This directory is the consumer handoff. CI uploads it as `q-macro-contract-${GITHUB_SHA}`.

## Pin

`DIGEST` is one iso3166/sha256sum line:

```text
<oracleDigest>  q-macro-contract
```

`manifest.json` repeats the same `oracleDigest`. Git SHA of the producer tree is provenance only; do not treat it as the pin.

`oracleDigest` is SHA-256 of canonical JSON (`sort_keys`, separators `,` `:`, no newline) of the file-byte SHA-256 map:

- `decision-vectors.json`
- `valid/program.json`
- `valid/weights.json`
- `valid/release.json`

The hex is not stored inside `decision-vectors.json`.

## How to run (consumer)

1. Pin `oracleDigest` from `DIGEST` / `manifest.json` (they must match).
2. Take the four oracle files from `corpus/testdata/traffic-contract/hotlink-macros/`.
3. Recompute the binding; fail closed on mismatch. Also check per-file SHA-256 in `manifest.json` / `MANIFEST.json`.
4. Prepare Program/Weights/Release with the existing bootstrap path (`prepareRelease`).
5. Run **every** `cases[]` from this `decision-vectors.json`. Deep-equal `expected`. Local consumer fixtures do not close the contract.

Recipe: `docs/control-plane/Q_MACRO_CONTRACT.md`.

## Out of scope

`m_campaign`, `m_country`, Activate-for-Activate, serving rebind.

Country-handoff (`docs/control-plane/evidence/traffic-country-handoff/`, `producerSha=10b0da8`) is a different freeze and is not modified here.

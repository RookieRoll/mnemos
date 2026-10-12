# Tasks

## 1. Vector recall source

- [x] 1.1 Add a scoped bulk read for live rows carrying embeddings (project/agent filters, deterministic order) to the vectorable store surface, with table-driven tests covering scope filters and rows missing embeddings; verify tests pass
- [x] 1.2 Add a benchmark for brute-force cosine over the scoped read (target scale ~10k rows) and record the measured latency ceiling in the design note; verify benchmark runs and reports ns/op

## 2. Normalization, fusion, and the score contract

- [x] 2.1 Replace the RRF scoring in the hybrid module with the two normalization transforms (saturating BM25, calibrated linear cosine clip) and weighted-mean relevance over available signals, wired through the existing hybrid params; verify unit tests cover each transform's endpoints and weight renormalization
- [x] 2.2 Rewrite the search fusion path so recall sources union before ranking (FTS candidates plus vector-recall candidates) and the composite score is produced at exactly one point; verify tests for the paraphrase-only recall scenario and the missing-embedding-not-penalized scenario from the spec
- [x] 2.3 Enforce and test the composite contract bounds (0, 1.2] and the multiplicative property (near-zero relevance cannot be lifted by policy factors); verify table-driven tests assert both
- [x] 2.4 Keep the retrieval-mode signal reporting contributed sources only, and verify the honesty tests assert mode changes neither score scale nor gating

## 3. Calibration

- [x] 3.1 Build a small deterministic calibration harness that sweeps the injection floor and the project-affinity penalty over the verify/retrieval fixtures plus surfaced-to-used ratios from the injection log; verify it emits a parameter report from a seeded store
- [x] 3.2 Set the floor and penalty constants from the calibration report, document the contract scale and the calibration basis in the architecture doc, and verify the hook tests assert the new-scale gate

## 4. Consumers and explainability

- [x] 4.1 Add score breakdown fields (relevance component, policy component) to the result type and surface them through the MCP tool result, HTTP API, CLI output, and the typed Go client; verify a test reproduces the composite from the reported components
- [x] 4.2 Add the hybrid-mode gate regression test: with embeddings active, an on-topic memory passes the prompt hook's gate and a weak batch injects nothing; verify the test fails on the pre-change code path and passes after
- [x] 4.3 Write the breaking-change release note (score semantics and range changed) and update the client package's score doc comments; verify the note is present and names the old and new scales

## 5. Integration verification

- [x] 5.1 Run the full test suite plus the retrieval verify mode, and confirm precision@K holds or improves versus the recorded baseline; verify with the test and verify commands' exit status and reported numbers

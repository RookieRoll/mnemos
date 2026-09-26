# Release notes

## Unreleased

### BREAKING: retrieval scores moved to the composite score contract

`mnemos_search`, `POST /v1/search`, `mnemos search`, and `pkg/client` now
return `Score` on the composite score contract scale:

- **Old scale** — a BM25-after-ranker magnitude (`~4-20` in keyword mode) or
  an RRF rank signal (`~0-0.016` in hybrid mode): two different scales
  behind one field, which made any absolute threshold wrong in one mode or
  the other.
- **New scale** — `Score = relevance x policy`, bounded to **`(0, 1.2]`**,
  identical across retrieval modes.

Consumers holding BM25-scale thresholds must move them onto the contract
scale (the prompt hook's floor is now `0.10`; the calibration procedure is
`mnemos verify calibrate`). The raw keyword magnitude is still returned per
hit as `BM25`, and the new `Relevance` / `PolicyFactor` fields carry the
breakdown.

Also in this release: vector similarity became a true recall source (unioned
with keyword candidates before ranking), so memories sharing no keywords
with the query are retrievable at all. Stores without an embedding provider
are unaffected beyond the score scale change: keyword ranking behaviour is
preserved and no data migration is needed.

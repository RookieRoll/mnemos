package memory

// EmbeddingBackfillInput selects one finite page of offline maintenance.
// IDs are ordered lexically; ThroughID is captured before the first page.
// This is a high-water traversal, not a transaction-wide data snapshot.
type EmbeddingBackfillInput struct {
	AfterID             string
	ThroughID           string
	Limit               int
	Model               string
	Dimension           int
	RebuildIncompatible bool
}

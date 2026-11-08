package fixture

import (
	"fmt"
	"strings"

	"github.com/aduverger/prunelle/server/internal/review"
)

// Manifest is the review content the implementing agent would push for the fixture feature.
func Manifest() review.Manifest {
	str := func(s string) *string { return &s }
	return review.Manifest{
		Author: review.Author{Kind: "AGENT", ID: "pi:fixture-session"},
		Intents: []review.ManifestIntent{
			{Key: "I-1", Kind: "BEHAVIOR", Statement: "Uploading a document returns promptly, regardless of document size.",
				Source: "Feature request: async document processing", AcceptanceCriteria: str("Upload responds within 500 ms at p95."),
				Evidence: []string{"E-spec"}},
			{Key: "I-2", Kind: "BEHAVIOR", Statement: "Users can follow the processing status of their document until it is done or failed.",
				Source: "Feature request: async document processing", Evidence: []string{"E-spec"}},
		},
		Evidence: []review.ManifestEvidence{
			codeRange("E-upload", "Upload enqueues instead of processing", "api", "src/routes/upload.ts", "HEAD", apiUploadWorktree,
				`await doc.setStatus("PENDING");`, `.json({ id: doc.id, status: "PENDING" });`),
			codeRange("E-queue", "Queue with job-level retries", "api", "src/queue.ts", "HEAD", apiQueue,
				`export async function enqueueProcessing`, `);`),
			codeRange("E-sync", "Former synchronous processing", "api", "src/processing.ts", "BASE", apiProcessingBase,
				`export async function processDocument`, `return summary;`),
			codeRange("E-status", "Status endpoint", "api", "src/routes/status.ts", "HEAD", apiStatus,
				`status.get(`, `});`),
			codeRange("E-retry", "Worker retry loop", "worker", "worker/consumer.py", "HEAD", workerConsumer,
				`for attempt in range`, `return`),
			codeRange("E-side-effect", "History write inside processing", "worker", "worker/processing.py", "HEAD", workerProcessing,
				`def process_document`, `return summarize`),
			codeRange("E-config", "Worker retry budget", "worker", "worker/config.py", "HEAD", workerConfigWorktree,
				`MAX_ATTEMPTS`, `MAX_ATTEMPTS`),
			codeRange("E-test-code", "Consumer test", "worker", "tests/test_consumer.py", "HEAD", workerConsumerTest,
				`def test_handle_marks_document_done`, `assert`),
			{Key: "E-test-run", Kind: "TEST_RESULT", Title: "pytest tests/test_consumer.py", Content: str(workerTestOutput),
				Source: "Ran by the implementing agent", Provenance: "MEASURED"},
			{Key: "E-spec", Kind: "SPECIFICATION", Title: "Upload latency and status requirements", Content: str(latencyRequirement),
				Source: "Feature request", Provenance: "OBSERVED"},
		},
		Changes: []review.ManifestChange{
			{Key: "C-1", Kind: "BEHAVIOR", Summary: "Upload enqueues processing and returns 202",
				Before:     "POST /documents processed the document inline and returned 201 with the summary.",
				After:      "POST /documents stores the document as PENDING, enqueues a job, and returns 202 with a status location.",
				Components: []string{"api"}, Evidence: []string{"E-upload", "E-queue", "E-sync"}},
			{Key: "C-2", Kind: "BEHAVIOR", Summary: "Worker consumes processing jobs with retries",
				Before:     "No background processing.",
				After:      "The worker marks documents PROCESSING, retries transient failures up to MAX_ATTEMPTS, then marks them DONE.",
				Components: []string{"worker", "queue"}, VerificationStatus: "SUPPORTED", Evidence: []string{"E-retry", "E-side-effect", "E-config", "E-test-run"}},
			{Key: "C-3", Kind: "INTERFACE", Summary: "New status endpoint",
				Before:     "Clients received the result in the upload response.",
				After:      "GET /documents/{id}/status returns the processing status.",
				Components: []string{"api"}, Evidence: []string{"E-status"}},
		},
		Findings: []review.ManifestFinding{
			{Key: "F-1", Kind: "CORRECTNESS", Severity: "HIGH", Title: "Retries can repeat processing side effects",
				Rationale: "process_document appends to the document history before it can fail, and both the queue (5 attempts) and the worker loop retry. A document may be processed up to 15 times without an idempotency guard.",
				Evidence:  []string{"E-retry", "E-side-effect", "E-queue"}},
			{Key: "F-2", Kind: "DESIGN", Severity: "MEDIUM", Title: "Failed documents stay PROCESSING forever",
				Rationale: "When every attempt fails, handle() returns without setting a FAILED status, so the status endpoint never reports failure.",
				Evidence:  []string{"E-retry", "E-status"}},
		},
		Topics: []review.ManifestTopic{
			{Key: "T-async", Title: "Asynchronous upload", Summary: "Uploads return immediately; processing moves to a queue consumed by the worker.",
				Intents: []string{"I-1"}, Changes: []string{"C-1"},
				Representations: []review.ManifestRepresentation{
					{Type: "TEXT", Text: &review.ManifestText{Sections: []review.ManifestSection{
						{Heading: "What changed", Body: "The upload handler no longer calls processDocument. It records the document as PENDING and enqueues a `process` job with 5 attempts and exponential backoff.", Evidence: []string{"E-upload", "E-queue"}},
						{Heading: "Why", Body: "Processing time grows with document size; the request must answer within 500 ms at p95.", Evidence: []string{"E-spec"}},
					}}},
					{Type: "DIAGRAM", Title: "Upload flow", Diagram: &review.ManifestDiagram{
						Nodes: []review.ManifestNode{
							{ID: "client", Label: "Client", Kind: "ACTOR"},
							{ID: "api", Label: "POST /documents", Kind: "SERVICE", Ref: &review.ManifestRef{Type: "CHANGE", Key: "C-1"}, Evidence: []string{"E-upload"}},
							{ID: "queue", Label: "document-processing queue", Kind: "QUEUE", Evidence: []string{"E-queue"}},
							{ID: "worker", Label: "Worker", Kind: "SERVICE", Ref: &review.ManifestRef{Type: "TOPIC", Key: "T-worker"}, Evidence: []string{"E-retry"}},
							{ID: "status", Label: "GET /documents/{id}/status", Kind: "SERVICE", Ref: &review.ManifestRef{Type: "CHANGE", Key: "C-3"}, Evidence: []string{"E-status"}},
						},
						Edges: []review.DiagramEdge{
							{From: "client", To: "api", Label: "upload"},
							{From: "api", To: "queue", Label: "enqueue"},
							{From: "queue", To: "worker", Label: "consume"},
							{From: "client", To: "status", Label: "poll"},
						},
					}},
					{Type: "CODE_DIFF"},
					{Type: "EVIDENCE_LIST"},
				}},
			{Key: "T-worker", Parent: "T-async", Title: "Background worker and retries", Summary: "The worker processes jobs and retries transient failures.",
				Changes: []string{"C-2"}, Findings: []string{"F-1"},
				Representations: []review.ManifestRepresentation{
					{Type: "TEXT", Text: &review.ManifestText{Sections: []review.ManifestSection{
						{Heading: "Retry behavior", Body: "Transient errors are retried in a loop inside the job, on top of the queue's own job attempts.", Evidence: []string{"E-retry", "E-queue"}},
					}}},
					{Type: "CODE_DIFF", Title: "Retry path", CodeDiff: &review.ManifestEvidenceList{Evidence: []string{"E-retry", "E-side-effect"}}},
				}},
			{Key: "T-status", Title: "Processing status flow", Summary: "How a user follows a document from upload to completion.",
				Intents: []string{"I-2"}, Changes: []string{"C-3"}, Findings: []string{"F-2"},
				Representations: []review.ManifestRepresentation{
					{Type: "TEXT", Text: &review.ManifestText{Sections: []review.ManifestSection{
						{Heading: "User flow", Body: "After uploading, the client polls the status endpoint until the document is DONE. No screenshots were collected; the UI was not validated visually.", Evidence: []string{"E-status"}},
					}}},
					{Type: "DIAGRAM", Title: "Status states", Diagram: &review.ManifestDiagram{
						Nodes: []review.ManifestNode{
							{ID: "pending", Label: "PENDING", Kind: "STATE", Evidence: []string{"E-upload"}},
							{ID: "processing", Label: "PROCESSING", Kind: "STATE", Evidence: []string{"E-retry"}},
							{ID: "done", Label: "DONE", Kind: "STATE", Evidence: []string{"E-retry"}},
							{ID: "failed", Label: "FAILED (never set)", Kind: "STATE", Ref: &review.ManifestRef{Type: "FINDING", Key: "F-2"}},
						},
						Edges: []review.DiagramEdge{
							{From: "pending", To: "processing", Label: "worker picks job"},
							{From: "processing", To: "done", Label: "success"},
							{From: "processing", To: "failed", Label: "attempts exhausted"},
						},
					}},
				}},
			{Key: "T-tests", Title: "Configuration and tests", Summary: "Retry budget and test coverage for the consumer.",
				Evidence: []string{"E-config", "E-test-code", "E-test-run"},
				Representations: []review.ManifestRepresentation{
					{Type: "TEXT", Text: &review.ManifestText{Sections: []review.ManifestSection{
						{Heading: "Coverage", Body: "One test covers the success path. Retry exhaustion and duplicate processing are untested.", Evidence: []string{"E-test-code", "E-test-run"}},
					}}},
				}},
		},
		Attention: []review.ManifestAttention{
			{Key: "A-1", Target: review.ManifestRef{Type: "FINDING", Key: "F-1"}, Priority: "HIGH",
				Rationale:   "Processing has side effects and runs under two independent retry mechanisms.",
				Uncertainty: "Whether duplicate history entries matter downstream is unknown.",
				Evidence:    []string{"E-retry", "E-side-effect"}},
		},
	}
}

// codeRange builds CODE_RANGE evidence spanning the first line containing first through the
// next line, at or after it, containing last.
func codeRange(key, title, repo, path, side, content, first, last string) review.ManifestEvidence {
	lines := strings.Split(content, "\n")
	start, end := -1, -1
	for i, line := range lines {
		if start < 0 && strings.Contains(line, first) {
			start = i
		}
		if start >= 0 && strings.Contains(line, last) {
			end = i
			break
		}
	}
	if start < 0 || end < 0 {
		panic(fmt.Sprintf("fixture %s: markers %q..%q not found in %s", key, first, last, path))
	}
	return review.ManifestEvidence{Key: key, Kind: "CODE_RANGE", Title: title,
		CodeRange: &review.ManifestCodeRange{Repository: repo, Path: path, Side: side, StartLine: start + 1, EndLine: end + 1}}
}

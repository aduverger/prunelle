package fixture

// File contents of the fixture repositories. The feature moves document processing from the
// API request into a background worker.

const apiPackageJSONBase = `{
  "name": "api",
  "version": "1.4.0",
  "dependencies": {
    "express": "^4.19.0"
  }
}
`

const apiPackageJSONFeature = `{
  "name": "api",
  "version": "1.4.0",
  "dependencies": {
    "bullmq": "^5.1.0",
    "express": "^4.19.0"
  }
}
`

const apiProcessingBase = `import { Document } from "./documents";

export async function processDocument(doc: Document): Promise<string> {
  const text = await extractText(doc);
  const summary = await summarize(text);
  await doc.save({ summary });
  return summary;
}
`

const apiUploadBase = `import { Router } from "express";
import { saveUpload } from "../documents";
import { processDocument } from "../processing";

export const upload = Router();

upload.post("/documents", async (req, res) => {
  const doc = await saveUpload(req);
  const summary = await processDocument(doc);
  res.status(201).json({ id: doc.id, summary });
});
`

const apiUploadCommitted = `import { Router } from "express";
import { saveUpload } from "../documents";
import { enqueueProcessing } from "../queue";

export const upload = Router();

upload.post("/documents", async (req, res) => {
  const doc = await saveUpload(req);
  await doc.setStatus("PENDING");
  await enqueueProcessing(doc.id);
  res.status(202).json({ id: doc.id, status: "PENDING" });
});
`

// apiUploadWorktree is the unstaged state of upload.ts.
const apiUploadWorktree = `import { Router } from "express";
import { saveUpload } from "../documents";
import { enqueueProcessing } from "../queue";

export const upload = Router();

upload.post("/documents", async (req, res) => {
  const doc = await saveUpload(req);
  await doc.setStatus("PENDING");
  await enqueueProcessing(doc.id);
  res
    .status(202)
    .location(` + "`/documents/${doc.id}/status`" + `)
    .json({ id: doc.id, status: "PENDING" });
});
`

const apiQueue = `import { Queue } from "bullmq";

const processing = new Queue("document-processing");

export async function enqueueProcessing(documentId: string): Promise<void> {
  await processing.add(
    "process",
    { documentId },
    { attempts: 5, backoff: { type: "exponential", delay: 1000 } },
  );
}
`

// apiStatus is staged, not committed.
const apiStatus = `import { Router } from "express";
import { findDocument } from "../documents";

export const status = Router();

status.get("/documents/:id/status", async (req, res) => {
  const doc = await findDocument(req.params.id);
  if (!doc) {
    res.status(404).end();
    return;
  }
  res.json({ id: doc.id, status: doc.status });
});
`

// apiDocsUntracked is never added to the index.
const apiDocsUntracked = `# Asynchronous upload

Uploads return 202 Accepted. Processing happens in the worker; poll the status endpoint.
`

const workerReadme = `# worker

Background jobs for the document platform.
`

const workerConfigBase = `QUEUE_NAME = "document-processing"
MAX_ATTEMPTS = 3
`

// workerConfigWorktree is the unstaged state of config.py.
const workerConfigWorktree = `QUEUE_NAME = "document-processing"
MAX_ATTEMPTS = 5
`

const workerConsumer = `from worker.config import MAX_ATTEMPTS
from worker.errors import TransientError
from worker.processing import process_document
from worker.store import documents


def handle(job):
    document_id = job.data["documentId"]
    documents.set_status(document_id, "PROCESSING")
    for attempt in range(1, MAX_ATTEMPTS + 1):
        try:
            summary = process_document(document_id)
        except TransientError:
            continue
        documents.save_summary(document_id, summary)
        documents.set_status(document_id, "DONE")
        return
`

const workerProcessing = `from worker.store import documents
from worker.text import extract_text, summarize


def process_document(document_id):
    document = documents.get(document_id)
    text = extract_text(document)
    documents.append_history(document_id, "extracted")
    return summarize(text)
`

// workerConsumerTest is staged, not committed.
const workerConsumerTest = `from worker.consumer import handle


def test_handle_marks_document_done(fake_job, fake_store):
    handle(fake_job("doc-1"))
    assert fake_store.status("doc-1") == "DONE"
`

const workerTestOutput = `$ pytest tests/test_consumer.py
============================= test session starts ==============================
collected 1 item

tests/test_consumer.py .                                                 [100%]

============================== 1 passed in 0.12s ===============================
`

const latencyRequirement = `Uploading a document must respond within 500 ms at p95, independent of document size.
Users must be able to see whether their document is pending, processing, done, or failed.
`

import type { Model } from './api/client';
import type { DiscussionTarget } from './model';
import { Attribution, EvidenceLinks } from './Views';

type Grounding = {
  evidence: Model['Evidence'][];
  open: (e: Model['Evidence']) => void;
  discuss: (target: DiscussionTarget) => void;
};

export function IntentCard({
  intent,
  revisionID,
  evidence,
  open,
  discuss,
}: Grounding & { intent: Model['Intent']; revisionID?: string }) {
  return (
    <article className="card">
      <div className="eyebrow">INTENT · {intent.kind}</div>
      <p>{intent.statement}</p>
      <p className="muted">Source: {intent.source}</p>
      {intent.acceptance_criteria && <p>Acceptance: {intent.acceptance_criteria}</p>}
      <Attribution author={intent.author} provenance={intent.provenance} />
      <EvidenceLinks ids={intent.evidence_ids} evidence={evidence} select={open} />
      <button
        className="link"
        disabled={!revisionID}
        onClick={() =>
          discuss({
            revisionID: revisionID!,
            anchor: { type: 'INTENT', id: intent.id },
            label: intent.statement,
          })
        }
      >
        Discuss intent
      </button>
    </article>
  );
}

export function ChangeCard({
  change,
  evidence,
  open,
  discuss,
}: Grounding & { change: Model['ObservedChange'] }) {
  return (
    <article className="card">
      <div className="eyebrow">OBSERVED CHANGE · {change.verification_status}</div>
      <h3>{change.summary}</h3>
      <p>Before: {change.before}</p>
      <p>After: {change.after}</p>
      <small className="muted">Revision: {change.revision_id}</small>
      <Attribution author={change.author} provenance={change.provenance} />
      <EvidenceLinks ids={change.evidence_ids} evidence={evidence} select={open} />
      <button
        className="link"
        onClick={() =>
          discuss({
            revisionID: change.revision_id,
            anchor: { type: 'CHANGE', id: change.id },
            label: change.summary,
          })
        }
      >
        Discuss change
      </button>
    </article>
  );
}

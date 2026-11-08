import { Markdown } from './Markdown';
import { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { MultiFileDiff, type SelectedLineRange } from '@pierre/diffs/react';
import ELK, { type ElkNode } from 'elkjs/lib/elk.bundled.js';
import { result, type API, type Model, type DiagramNode } from './api/client';
import type { DiscussionTarget, FileSelection, View } from './model';

export function Attribution({
  author,
  provenance,
}: {
  author: Model['Author'];
  provenance?: string;
}) {
  return (
    <small className="attribution">
      {provenance && <span className={'badge ' + provenance.toLowerCase()}>{provenance}</span>}{' '}
      <span className="badge">{author.kind}</span> {author.id}
    </small>
  );
}
export function Notice({ children }: { children: React.ReactNode }) {
  return <p className="notice">{children}</p>;
}
export function ErrorNotice({ error }: { error: Error | null }) {
  return (
    error && (
      <p role="alert" className="error">
        {error.message}
      </p>
    )
  );
}
export function EvidenceLinks({
  ids,
  evidence,
  select,
}: {
  ids: string[];
  evidence: Model['Evidence'][];
  select: (e: Model['Evidence']) => void;
}) {
  return (
    <div className="evidence-links">
      {ids.map((id) => {
        const e = evidence.find((e) => e.id === id);
        return e ? (
          <button key={id} className="link" onClick={() => select(e)}>
            ↗ {e.title}
          </button>
        ) : (
          <span key={id}>Evidence unavailable: {id}</span>
        );
      })}
    </div>
  );
}

export function EvidenceCard({
  evidence: e,
  open,
  discuss,
}: {
  evidence: Model['Evidence'];
  open: (e: Model['Evidence']) => void;
  discuss: (t: DiscussionTarget) => void;
}) {
  let artifact: React.ReactNode;
  if (e.code_range) {
    artifact = (
      <>
        <button className="link" onClick={() => open(e)}>
          {e.code_range.path}:{e.code_range.start_line}–{e.code_range.end_line} ·{' '}
          {e.code_range.side}
        </button>
        <pre>{e.code_range.selected_text}</pre>
      </>
    );
  } else if (e.content) {
    artifact = <pre>{e.content}</pre>;
  } else {
    artifact = <Notice>No artifact was provided. This is not verification.</Notice>;
  }
  return (
    <article className="card" data-testid="evidence-card">
      <div className="eyebrow">
        {e.kind.replaceAll('_', ' ')} · {e.revision_id}
      </div>
      <h3>{e.title}</h3>
      <Attribution author={e.author} provenance={e.provenance} />
      <p className="muted">Source: {e.source || 'Not provided'}</p>
      {artifact}
      <button
        onClick={() =>
          discuss({
            revisionID: e.revision_id,
            anchor: { type: 'EVIDENCE', id: e.id },
            label: e.title,
          })
        }
      >
        Discuss evidence
      </button>
    </article>
  );
}

export function Diagram({
  representation,
  select,
  discuss,
  elementID,
}: {
  elementID: string | null;
  representation: Model['Representation'];
  select: (node: DiagramNode) => void;
  discuss: (t: DiscussionTarget) => void;
}) {
  const [layout, setLayout] = useState<ElkNode>();
  const [error, setError] = useState<Error | null>(null);
  const config = representation.diagram;
  useEffect(() => {
    let active = true;
    setLayout(undefined);
    setError(null);
    if (!config) return;
    const elk = new ELK();
    void elk
      .layout({
        id: 'root',
        layoutOptions: {
          'elk.algorithm': 'layered',
          'elk.direction': 'DOWN',
          'elk.spacing.nodeNode': '36',
          'elk.layered.spacing.nodeNodeBetweenLayers': '62',
        },
        children: config.nodes.map((n) => ({ id: n.id, width: 210, height: 70 })),
        edges: config.edges.map((e, i) => ({
          id: String(i),
          sources: [e.from],
          targets: [e.to],
          labels: [{ text: e.label, width: 150, height: 20 }],
        })),
      })
      .then((value) => {
        if (active) setLayout(value);
      })
      .catch((e) => {
        if (active) setError(e);
      });
    return () => {
      active = false;
    };
  }, [config]);
  if (error) return <ErrorNotice error={error} />;
  if (!layout) return <Notice>Laying out diagram…</Notice>;
  return (
    <div className="diagram">
      <svg
        role="img"
        aria-label={representation.title}
        viewBox={`0 0 ${layout.width} ${layout.height}`}
      >
        {layout.edges?.map((edge) => (
          <g key={edge.id}>
            {edge.sections?.map((s, i) => (
              <path
                key={i}
                d={
                  'M' +
                  [s.startPoint, ...(s.bendPoints || []), s.endPoint]
                    .map((p) => `${p.x},${p.y}`)
                    .join(' L')
                }
                fill="none"
                stroke="currentColor"
              />
            ))}
            {edge.labels?.map((l, i) => (
              <text key={i} x={l.x} y={(l.y || 0) + 14} className="edge-label">
                {l.text}
              </text>
            ))}
          </g>
        ))}
        {layout.children?.map((n) => {
          const node = config!.nodes.find((c) => c.id === n.id)!;
          return (
            <g
              key={n.id}
              transform={`translate(${n.x},${n.y})`}
              role="button"
              tabIndex={0}
              aria-pressed={elementID === node.id}
              aria-label={'Inspect ' + node.label}
              onClick={() => select(node)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  select(node);
                }
              }}
            >
              <rect width={n.width} height={n.height} rx="8" />
              <text x="14" y="23" className="node-kind">
                {node.kind}
              </text>
              <text x="14" y="48">
                {node.label.length > 27 ? node.label.slice(0, 26) + '…' : node.label}
              </text>
              <title>{node.label}</title>
            </g>
          );
        })}
      </svg>
      <div className="node-list">
        {config!.nodes.map((node) => (
          <div key={node.id}>
            <button className="link" onClick={() => select(node)}>
              {node.label}
            </button>
            {representation.id && representation.revision_id && (
              <button
                className="quiet"
                aria-label={'Discuss ' + node.label}
                onClick={() =>
                  discuss({
                    revisionID: representation.revision_id!,
                    anchor: {
                      type: 'DIAGRAM_ELEMENT',
                      id: representation.id!,
                      element_id: node.id,
                    },
                    label: node.label,
                  })
                }
              >
                Discuss
              </button>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

export function FileDiff({
  api,
  file,
  repositoryLabel,
  choose,
  discuss,
}: {
  api: API;
  file: FileSelection;
  repositoryLabel?: string;
  choose: (f: FileSelection) => void;
  discuss: (t: DiscussionTarget) => void;
}) {
  const query = useQuery({
    queryKey: ['file', file.revisionID, file.repositoryID, file.path],
    queryFn: ({ signal }) =>
      result(
        api.GET('/v1/revisions/{revisionId}/file', {
          params: {
            path: { revisionId: file.revisionID },
            query: { repository_id: file.repositoryID, path: file.path },
          },
          signal,
        }),
      ),
  });
  const content = query.data;
  const oldFile = useMemo(
    () =>
      content?.base_text == null
        ? null
        : { name: content.old_path || file.path, contents: content.base_text },
    [content, file.path],
  );
  const newFile = useMemo(
    () => (content?.head_text == null ? null : { name: file.path, contents: content.head_text }),
    [content, file.path],
  );
  const selected: SelectedLineRange | null = file.range
    ? {
        start: file.range.start_line,
        end: file.range.end_line,
        side: file.range.side === 'BASE' ? 'deletions' : 'additions',
      }
    : null;
  function select(range: SelectedLineRange | null) {
    if (!range || (range.endSide && range.endSide !== range.side)) return;
    choose({
      ...file,
      range: {
        repository_id: file.repositoryID,
        path: file.path,
        side: range.side === 'deletions' ? 'BASE' : 'HEAD',
        start_line: Math.min(range.start, range.end),
        end_line: Math.max(range.start, range.end),
      },
    });
  }
  if (!content)
    return (
      <>
        <ErrorNotice error={query.error} />
        <Notice>{query.isPending ? 'Loading stored source…' : 'Source unavailable.'}</Notice>
      </>
    );
  return (
    <section className="file-diff" data-testid="file-diff">
      <div className="file-heading">
        <strong>
          {repositoryLabel || file.repositoryID}/{file.path}
        </strong>
        <span className="badge">{content.status}</span>
      </div>
      <small className="muted">
        Immutable snapshot · {file.revisionID}
        {content.old_path && ' · renamed from ' + content.old_path}
      </small>
      {content.binary || content.content_omitted ? (
        <Notice>{content.content_omitted || 'Binary file: no text preview.'}</Notice>
      ) : (
        <>
          <p className="muted">Select line numbers to discuss a range on one side.</p>
          <MultiFileDiff
            {...(oldFile ? { oldFile, newFile } : { oldFile: null, newFile: newFile! })}
            selectedLines={selected}
            options={{
              theme: 'github-light',
              diffStyle: 'unified',
              enableLineSelection: true,
              onLineSelectionEnd: select,
              expandUnchanged: true,
            }}
          />
          {file.range && (
            <div className="range-selection">
              <span>
                {file.range.side} lines {file.range.start_line}–{file.range.end_line}
              </span>
              <button
                onClick={() => {
                  const { repository_id, path, side, start_line, end_line } = file.range!;
                  discuss({
                    revisionID: file.revisionID,
                    anchor: {
                      type: 'CODE_RANGE',
                      code_range: { repository_id, path, side, start_line, end_line },
                    },
                    label: `${file.path}:${start_line}–${end_line} (${side})`,
                  });
                }}
              >
                Discuss selected lines
              </button>
            </div>
          )}
        </>
      )}
    </section>
  );
}

export function RepresentationView({
  type,
  representationID,
  chooseRepresentation,
  elementID,
  representations,
  evidence,
  file,
  repositoryLabel,
  api,
  chooseFile,
  openEvidence,
  selectNode,
  discuss,
}: {
  type: View;
  representationID?: string;
  chooseRepresentation: (id: string) => void;
  elementID: string | null;
  representations: Model['Representation'][];
  evidence: Model['Evidence'][];
  file: FileSelection | null;
  repositoryLabel?: string;
  api: API;
  chooseFile: (f: FileSelection) => void;
  openEvidence: (e: Model['Evidence']) => void;
  selectNode: (n: DiagramNode) => void;
  discuss: (t: DiscussionTarget) => void;
}) {
  const matches = representations.filter((r) => r.type === type);
  const rep = matches.find((r) => (r.id || r.type) === representationID) || matches[0];
  const ids = rep?.code_diff?.evidence_ids || rep?.evidence_list?.evidence_ids || [];
  const items = ids
    .map((id) => evidence.find((e) => e.id === id))
    .filter((e): e is Model['Evidence'] => !!e);
  let content: React.ReactNode;
  if (type === 'CODE_DIFF' && file) {
    content = (
      <>
        {items.length > 0 && (
          <details>
            <summary>Linked code ranges</summary>
            <EvidenceLinks ids={ids} evidence={evidence} select={openEvidence} />
          </details>
        )}
        <FileDiff
          api={api}
          file={file}
          repositoryLabel={repositoryLabel}
          choose={chooseFile}
          discuss={discuss}
        />
      </>
    );
  } else if (!rep || !rep.available) {
    content = (
      <Notice>
        {rep?.unavailable_reason || 'No representation is available for this selection.'}
      </Notice>
    );
  } else {
    content = (
      <>
        <div className="representation-heading">
          <h3>{rep.title}</h3>
          <span className="badge">{rep.origin}</span>
        </div>
        {rep.author && <Attribution author={rep.author} />}
        {type === 'TEXT' &&
          rep.text?.sections.map((section, i) => (
            <section className="text-section" key={i}>
              <h3>{section.heading}</h3>
              <Markdown>{section.body}</Markdown>
              <EvidenceLinks ids={section.evidence_ids} evidence={evidence} select={openEvidence} />
            </section>
          ))}
        {type === 'DIAGRAM' && (
          <Diagram
            representation={rep}
            elementID={elementID}
            select={selectNode}
            discuss={discuss}
          />
        )}
        {type === 'CODE_DIFF' && (
          <div className="file-choices">
            {items.map((e) => (
              <button key={e.id} onClick={() => openEvidence(e)}>
                {e.code_range?.path} · {e.title}
              </button>
            ))}
          </div>
        )}
        {type === 'EVIDENCE_LIST' &&
          items.map((e) => (
            <EvidenceCard key={e.id} evidence={e} open={openEvidence} discuss={discuss} />
          ))}
      </>
    );
  }
  return (
    <div className="representation">
      {matches.length > 1 && (
        <select
          aria-label="Representation"
          value={rep?.id || rep?.type}
          onChange={(e) => chooseRepresentation(e.target.value)}
        >
          {matches.map((r) => (
            <option key={r.id || r.type} value={r.id || r.type}>
              {r.title}
            </option>
          ))}
        </select>
      )}
      {content}
    </div>
  );
}

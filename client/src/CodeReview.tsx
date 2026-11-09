import { useEffect, useMemo, useRef, useState } from 'react';
import { useQueries } from '@tanstack/react-query';
import {
  canUseWASM,
  getHighlighterType,
  parseDiffFromFile,
  type CodeViewDiffItem,
  type CodeViewLineSelection,
  type DiffLineAnnotation,
  type SelectedLineRange,
  type HighlighterTypes,
  preloadHighlighter,
} from '@pierre/diffs';
import {
  CodeView,
  useStableCallback,
  type CodeViewHandle,
  type CodeViewProps,
} from '@pierre/diffs/react';
import { result, type API, type Model } from './api/client';
import type { FileSelection } from './model';
import { DiscussionSlot, useDiscussions } from './Discussions';
import { ErrorNotice, Notice } from './Views';

export function sourceFileID(file: FileSelection) {
  return JSON.stringify([file.revisionID, file.repositoryID, file.path]);
}
const preferredHighlighter: HighlighterTypes = canUseWASM() ? 'highlights' : 'shiki-js';
type LoadedFile = Model['FileContent'] & {
  fileDiff: ReturnType<typeof parseDiffFromFile> | null;
  id: string;
};
type Presentation = CodeViewDiffItem<string[]> & { annotationKey: string };

function samePresentation(item: Presentation, previous: Presentation | undefined) {
  return (
    previous?.id === item.id &&
    previous.annotationKey === item.annotationKey &&
    previous.collapsed === item.collapsed
  );
}

function parseStoredFile(content: Model['FileContent']): LoadedFile {
  const id = sourceFileID({
    revisionID: content.revision_id,
    repositoryID: content.repository_id,
    path: content.path,
  });
  const oldFile =
    content.base_text == null
      ? null
      : {
          name: content.old_path || content.path,
          contents: content.base_text,
        };
  const newFile =
    content.head_text == null
      ? null
      : {
          name: content.path,
          contents: content.head_text,
        };
  const fileDiff =
    content.binary || content.content_omitted || (!oldFile && !newFile)
      ? null
      : parseDiffFromFile(oldFile, newFile);
  if (fileDiff) fileDiff.cacheKey = id;
  return { ...content, id, fileDiff };
}

export function CodeReview({
  api,
  files,
  file,
  choose,
  navigation,
  repositories,
  header,
  loading,
  error,
  openDraft,
}: {
  api: API;
  files: FileSelection[];
  file: FileSelection | null;
  choose: (file: FileSelection) => void;
  navigation: number;
  repositories: Model['Revision']['snapshots'];
  header?: React.ReactNode;
  loading?: boolean;
  error?: Error | null;
  openDraft: () => void;
}) {
  const { composer, discussions } = useDiscussions();
  const viewer = useRef<CodeViewHandle<string[], undefined>>(null);
  const [backend, setBackend] = useState<HighlighterTypes>();
  const [highlightError, setHighlightError] = useState<Error | null>(null);
  useEffect(() => {
    let active = true;
    void preloadHighlighter({ preferredHighlighter, themes: ['pierre-light'], langs: [] })
      .then(() => {
        if (active) setBackend(getHighlighterType());
      })
      .catch((error: Error) => {
        if (active) setHighlightError(error);
      });
    return () => {
      active = false;
    };
  }, []);
  const lastNavigation = useRef(-1);
  const [style, setStyle] = useState<'split' | 'unified'>('split');
  const [selectionError, setSelectionError] = useState('');
  const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
  const loaded = useQueries({
    queries: files.map(({ revisionID, repositoryID, path }) => ({
      queryKey: ['file', revisionID, repositoryID, path],
      queryFn: ({ signal }: { signal: AbortSignal }) =>
        result(
          api.GET('/v1/revisions/{revisionId}/file', {
            params: {
              path: { revisionId: revisionID },
              query: { repository_id: repositoryID, path },
            },
            signal,
          }),
        ),
      select: parseStoredFile,
    })),
  });
  const annotations = new Map<string, DiffLineAnnotation<string[]>[]>();
  function annotate(id: string, range: NonNullable<FileSelection['range']>, metadata: string) {
    const values = annotations.get(id) || [];
    const side = range.side === 'BASE' ? 'deletions' : 'additions';
    const atLine = values.find(
      (value) => value.side === side && value.lineNumber === range.end_line,
    );
    if (atLine) atLine.metadata.push(metadata);
    else values.push({ side, lineNumber: range.end_line, metadata: [metadata] });
    annotations.set(id, values);
  }
  for (const discussion of discussions) {
    const range = discussion.anchor.code_range;
    if (range)
      annotate(
        sourceFileID({
          revisionID: discussion.revision_id,
          repositoryID: range.repository_id,
          path: range.path,
        }),
        range,
        discussion.id,
      );
  }
  const draft = composer.target;
  if (draft?.anchor.code_range) {
    const range = draft.anchor.code_range;
    annotate(
      sourceFileID({
        revisionID: draft.revisionID,
        repositoryID: range.repository_id,
        path: range.path,
      }),
      range,
      'composer',
    );
  }
  const projected: Presentation[] = loaded.flatMap((query) => {
    const content = query.data;
    if (!content?.fileDiff) return [];
    const values = annotations.get(content.id) || [];
    return [
      {
        id: content.id,
        type: 'diff',
        fileDiff: content.fileDiff,
        annotations: values,
        annotationKey: JSON.stringify(values),
        collapsed: collapsed.has(content.id),
      },
    ];
  });
  const [items, setItems] = useState<Presentation[]>([]);
  if (
    items.length !== projected.length ||
    projected.some((item, index) => !samePresentation(item, items[index]))
  ) {
    setItems(
      projected.map((item) => {
        const previous = items.find((value) => value.id === item.id);
        return samePresentation(item, previous)
          ? previous!
          : { ...item, version: (previous?.version ?? -1) + 1 };
      }),
    );
  }
  const selectedID = file ? sourceFileID(file) : null;
  const selectedLines = useMemo<CodeViewLineSelection | null>(
    () =>
      file?.range
        ? {
            id: sourceFileID(file),
            range: {
              start: file.range.start_line,
              end: file.range.end_line,
              side: file.range.side === 'BASE' ? 'deletions' : 'additions',
            },
          }
        : null,
    [file],
  );
  const fileFromSelection = (selection: CodeViewLineSelection): FileSelection | null => {
    const range = selection.range;
    if (range.endSide && range.endSide !== range.side) {
      setSelectionError('A code discussion must select lines on one side: BASE or HEAD.');
      return null;
    }
    setSelectionError('');
    const identity = files.find((value) => sourceFileID(value) === selection.id);
    return identity
      ? {
          ...identity,
          range: {
            repository_id: identity.repositoryID,
            path: identity.path,
            side: range.side === 'deletions' ? 'BASE' : 'HEAD',
            start_line: Math.min(range.start, range.end),
            end_line: Math.max(range.start, range.end),
          },
        }
      : null;
  };
  const select = useStableCallback((selection: CodeViewLineSelection | null) => {
    const selectedFile = selection && fileFromSelection(selection);
    if (selectedFile) choose(selectedFile);
  });
  const begin = useStableCallback((id: string, range: SelectedLineRange, reveal = false) => {
    const selectedFile = fileFromSelection({ id, range });
    if (!selectedFile?.range || !items.some((item) => item.id === id)) return;
    const codeRange = selectedFile.range;
    choose(selectedFile);
    if (reveal)
      viewer.current?.scrollTo({
        type: 'line',
        id,
        lineNumber: codeRange.end_line,
        side: range.side,
        align: 'center',
        behavior: 'instant',
      });
    composer.start({
      revisionID: selectedFile.revisionID,
      anchor: { type: 'CODE_RANGE', code_range: codeRange },
      label: `${selectedFile.path}:${codeRange.start_line}–${codeRange.end_line} (${codeRange.side})`,
    });
  });
  const options = useMemo(
    () => ({
      theme: 'pierre-light' as const,
      themeType: 'light' as const,
      preferredHighlighter,
      diffStyle: style,
      enableLineSelection: true,
      enableGutterUtility: true,
      expandUnchanged: true,
      stickyHeaders: true,
      lineHoverHighlight: 'number' as const,
      layout: { paddingTop: 12, paddingBottom: 16, gap: 16 },
    }),
    [style],
  );
  const renderAnnotation = useStableCallback<
    NonNullable<CodeViewProps<string[], undefined>['renderAnnotation']>
  >((annotation, displayed, item) => {
    if (
      item.type !== 'diff' ||
      !('cacheKey' in displayed) ||
      displayed.cacheKey !== item.fileDiff.cacheKey
    )
      return null;
    return (
      <>
        {annotation.metadata.map((id) => (
          <DiscussionSlot key={id} id={id} />
        ))}
      </>
    );
  });
  const renderGutterUtility = useStableCallback<
    NonNullable<CodeViewProps<string[], undefined>['renderGutterUtility']>
  >((getHoveredLine, item) => (
    <button
      type="button"
      className="gutter-comment"
      aria-label="Add line comment"
      title="Add line comment"
      onPointerDown={(event) => event.stopPropagation()}
      onClick={() => {
        const selected = viewer.current?.getSelectedLines();
        const hovered = getHoveredLine();
        if (selected?.id === item.id) begin(item.id, selected.range);
        else if (hovered)
          begin(item.id, {
            start: hovered.lineNumber,
            end: hovered.lineNumber,
            side: 'side' in hovered && hovered.side === 'deletions' ? 'deletions' : 'additions',
          });
      }}
    >
      +
    </button>
  ));
  const renderHeaderPrefix = useStableCallback((item: { id: string }) => {
    const identity = files.find((value) => sourceFileID(value) === item.id);
    const label =
      repositories.find((repository) => repository.repository_id === identity?.repositoryID)
        ?.repository_label || identity?.repositoryID;
    return (
      <span
        className="badge"
        data-source-path={identity?.path}
        data-repository-id={identity?.repositoryID}
      >
        {label}
      </span>
    );
  });
  const renderHeaderFilenameSuffix = useStableCallback((item: { id: string }) => (
    <span className="row">
      <span className="badge">
        {loaded.find((query) => query.data?.id === item.id)?.data?.status}
      </span>
      <button
        className="quiet"
        aria-label={
          (collapsed.has(item.id) ? 'Expand ' : 'Collapse ') +
          files.find((f) => sourceFileID(f) === item.id)?.path
        }
        onClick={() =>
          setCollapsed((previous) => {
            const next = new Set(previous);
            if (next.has(item.id)) next.delete(item.id);
            else next.add(item.id);
            return next;
          })
        }
      >
        {collapsed.has(item.id) ? 'Expand' : 'Collapse'}
      </button>
    </span>
  ));
  useEffect(() => {
    if (!file) {
      lastNavigation.current = navigation;
      return;
    }
    if (navigation === lastNavigation.current || loading || loaded.some((query) => query.isPending))
      return;
    const id = sourceFileID(file);
    const item = items.find((value) => value.id === id);
    if (!item) return;
    if (item.collapsed) {
      setCollapsed((previous) => {
        const next = new Set(previous);
        next.delete(id);
        return next;
      });
      return;
    }
    viewer.current?.scrollTo(
      selectedLines
        ? {
            type: 'range',
            id,
            range: selectedLines.range,
            align: 'center',
            behavior: 'instant',
          }
        : { type: 'item', id, align: 'start', behavior: 'instant' },
    );
    lastNavigation.current = navigation;
  }, [file, navigation, items, loaded, loading, selectedLines]);
  const selectedFileIndex = files.findIndex((file) => sourceFileID(file) === selectedID);
  const selectedQuery = loaded[selectedFileIndex];
  const selectedContent = selectedQuery?.data;
  const selectedError = selectedQuery?.error;
  useEffect(() => {
    if (selectedError && !selectedContent && navigation !== lastNavigation.current) {
      viewer.current?.scrollTo({ type: 'position', position: 0, behavior: 'instant' });
    } else if (
      selectedContent &&
      !selectedContent.fileDiff &&
      navigation !== lastNavigation.current
    ) {
      viewer.current?.scrollTo({ type: 'position', position: 0, behavior: 'instant' });
      lastNavigation.current = navigation;
    }
  }, [selectedContent, selectedError, navigation]);
  const sourceError = error || loaded.find((query) => query.error)?.error;
  const renderHeader = () => (
    <div className="code-context">
      {header}
      <p className="muted">
        Immutable snapshot · {file?.revisionID || files[0]?.revisionID || 'No snapshot'} ·{' '}
        {files.length} files
      </p>
      <ErrorNotice error={sourceError || highlightError || null} />
      {(loading || loaded.some((query) => query.isPending)) && (
        <Notice>Loading stored source…</Notice>
      )}
      {selectedContent && !selectedContent.fileDiff && (
        <section className="unavailable-file">
          <h3>
            {repositories.find((r) => r.repository_id === file?.repositoryID)?.repository_label}/
            {file?.path}
          </h3>
          <span className="badge">{selectedContent.status}</span>
          <Notice>
            {selectedContent.content_omitted ||
              (selectedContent.binary ? 'Binary file: no text preview.' : 'Source unavailable.')}
          </Notice>
        </section>
      )}
      {files.map((file, index) => {
        if (!loaded[index]?.error || loaded[index]?.data) return null;
        const slots =
          annotations.get(sourceFileID(file))?.flatMap((annotation) => annotation.metadata) || [];
        return slots.length ? (
          <section key={sourceFileID(file)} className="unavailable-discussions">
            <h3>Original source unavailable · {file.path}</h3>
            <Notice>Messages and drafts retain their original snapshot anchor.</Notice>
            {slots.map((id) => (
              <DiscussionSlot key={id} id={id} />
            ))}
          </section>
        ) : null;
      })}
      {!files.length && !loading && <Notice>No stored source changes in this scope.</Notice>}
    </div>
  );
  return (
    <section className="code-review" data-testid="file-diff" data-highlighter={backend}>
      <div className="code-toolbar">
        <div className="row" aria-label="Diff layout">
          <button aria-pressed={style === 'split'} onClick={() => setStyle('split')}>
            Split
          </button>
          <button aria-pressed={style === 'unified'} onClick={() => setStyle('unified')}>
            Unified
          </button>
        </div>
        {file?.range && (
          <span>
            {file.range.side} lines {file.range.start_line}–{file.range.end_line}
          </span>
        )}
        {selectionError && (
          <p role="alert" className="error">
            {selectionError}
          </p>
        )}
        <button
          disabled={!selectedLines || !items.some((item) => item.id === selectedLines.id)}
          onClick={() => {
            if (selectedLines) begin(selectedLines.id, selectedLines.range, true);
          }}
        >
          Comment on selected lines
        </button>
        {draft && (
          <button className="link" onClick={openDraft}>
            Return to draft
          </button>
        )}
        <LineCommentForm
          file={files[selectedID ? selectedFileIndex : 0]}
          content={selectedID ? selectedContent : loaded[0]?.data}
          begin={begin}
        />
      </div>
      <CodeView
        ref={viewer}
        items={items}
        options={options}
        className="code-viewport"
        selectedLines={selectedLines}
        onSelectedLinesChange={select}
        renderAnnotation={renderAnnotation}
        renderGutterUtility={renderGutterUtility}
        renderHeaderPrefix={renderHeaderPrefix}
        renderHeaderFilenameSuffix={renderHeaderFilenameSuffix}
        renderCodeViewHeader={renderHeader}
        renderCodeViewFooter={() => (
          <p className="code-footer muted">End of stored source · {files.length} files</p>
        )}
      />
    </section>
  );
}

function LineCommentForm({
  file,
  content,
  begin,
}: {
  file: FileSelection | undefined;
  content: LoadedFile | undefined;
  begin: (id: string, range: SelectedLineRange, reveal?: boolean) => void;
}) {
  const [side, setSide] = useState<'BASE' | 'HEAD'>('HEAD');
  const [start, setStart] = useState(1);
  const [end, setEnd] = useState('');
  const [error, setError] = useState('');
  const id = file ? sourceFileID(file) : null;
  return (
    <details className="line-comment-form">
      <summary>Comment on lines…</summary>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          const text = side === 'BASE' ? content?.base_text : content?.head_text;
          const last = end ? Number(end) : start;
          const count = text?.split('\n').length;
          if (
            !id ||
            !content?.fileDiff ||
            text == null ||
            !count ||
            last < start ||
            last > count - (text.endsWith('\n') ? 1 : 0)
          ) {
            setError('Select a stored text file and a valid range on the chosen side.');
            return;
          }
          setError('');
          event.currentTarget.parentElement?.removeAttribute('open');
          begin(id, { start, end: last, side: side === 'BASE' ? 'deletions' : 'additions' }, true);
        }}
      >
        <p>{file?.path || 'Select a file first.'}</p>
        <label>
          Side
          <select
            aria-label="Comment side"
            value={side}
            onChange={(event) => setSide(event.target.value as typeof side)}
          >
            <option>HEAD</option>
            <option>BASE</option>
          </select>
        </label>
        <label>
          Start line
          <input
            aria-label="Start line"
            type="number"
            min={1}
            required
            value={start}
            onChange={(event) => setStart(Number(event.target.value))}
          />
        </label>
        <label>
          End line
          <input
            aria-label="End line"
            type="number"
            min={start}
            value={end}
            placeholder="Same line"
            onChange={(event) => setEnd(event.target.value)}
          />
        </label>
        <button type="submit">Add comment on lines</button>
        {error && (
          <p role="alert" className="error">
            {error}
          </p>
        )}
      </form>
    </details>
  );
}

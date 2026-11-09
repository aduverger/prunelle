import { useEffect } from 'react';
import { useStableCallback } from '@pierre/diffs/react';
import type {
  FileTreeRowDecorationRenderer,
  FileTreeSelectionChangeListener,
  GitStatus,
} from '@pierre/trees';
import { FileTree, useFileTree, useFileTreeSelector } from '@pierre/trees/react';
import type { Model } from './api/client';
import type { FileSelection } from './model';

type RepositorySource = Model['SourceChanges']['repositories'][number];
const gitStatusByFileStatus: Record<Model['FileStatus'], GitStatus> = {
  ADDED: 'added',
  MODIFIED: 'modified',
  DELETED: 'deleted',
  RENAMED: 'renamed',
  TYPE_CHANGED: 'modified',
  UNTRACKED: 'untracked',
};

function RepositoryFiles({
  files,
  repositoryID,
  revisionID,
  file,
  choose,
}: {
  files: RepositorySource['files'];
  repositoryID: string;
  revisionID: string;
  file: FileSelection | null;
  choose: (f: FileSelection) => void;
}) {
  const selectedPath =
    file?.revisionID === revisionID && file.repositoryID === repositoryID ? file.path : null;
  const renderRowDecoration = useStableCallback<FileTreeRowDecorationRenderer>(({ item }) => {
    const f = files.find((f) => f.path === item.path);
    if (!f) return null;
    const labels = [];
    if (f.status === 'TYPE_CHANGED') labels.push('Type changed');
    if (f.unassigned) labels.push('Unassigned');
    const text = labels.join(' · ');
    return text ? { text, title: `${text} · ${f.status}` } : null;
  });
  const onSelectionChange = useStableCallback<FileTreeSelectionChangeListener>((selectedPaths) => {
    const path = selectedPaths.at(-1);
    if (!path || path === selectedPath || !files.some((f) => f.path === path)) return;
    choose({ revisionID, repositoryID, path });
  });
  const { model } = useFileTree({
    paths: files.map((f) => f.path),
    initialExpansion: 'open',
    flattenEmptyDirectories: false,
    search: true,
    renaming: false,
    dragAndDrop: false,
    gitStatus: files.map((f) => ({ path: f.path, status: gitStatusByFileStatus[f.status] })),
    renderRowDecoration,
    onSelectionChange,
  });
  function reactivateFile(path: string | null | undefined) {
    if (path && path === selectedPath && model.getItem(path)?.isDirectory() === false)
      choose({ revisionID, repositoryID, path });
  }
  const height = useFileTreeSelector(
    model,
    (tree) => Math.max(1, tree.getVisibleCount()) * tree.getItemHeight() + 48,
  );
  useEffect(() => {
    for (const path of model.getSelectedPaths()) {
      if (path !== selectedPath) model.getItem(path)?.deselect();
    }
    const item = selectedPath ? model.getItem(selectedPath) : null;
    if (item) {
      item.select();
      model.scrollToPath(item.getPath(), { focus: false });
    }
  }, [model, selectedPath]);
  return (
    <FileTree
      model={model}
      className="repository-files"
      style={{ height: Math.min(height, 320) }}
      onClickCapture={(event) => {
        if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
        const row = event.nativeEvent
          .composedPath()
          .find((node) => node instanceof HTMLElement && node.getAttribute('role') === 'treeitem');
        reactivateFile(row instanceof HTMLElement ? row.dataset.itemPath : undefined);
      }}
      onKeyDownCapture={(event) => {
        if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
        if (event.key === 'Enter' && model.isSearchOpen()) reactivateFile(model.getFocusedPath());
      }}
    />
  );
}

export function SourceTree({
  source,
  file,
  choose,
}: {
  source: Model['SourceChanges'];
  file: FileSelection | null;
  choose: (f: FileSelection) => void;
}) {
  function renderRepositories(unassignedOnly: boolean) {
    return source.repositories.map((repository) => {
      const files = unassignedOnly
        ? repository.files.filter((f) => f.unassigned)
        : repository.files;
      if (unassignedOnly && !files.length) return null;
      return (
        <section key={repository.repository_id} aria-label={repository.repository_label}>
          <h3>
            {repository.repository_label} <span className="count">{files.length}</span>
          </h3>
          {!files.length && <p className="muted">No changed files.</p>}
          {(['Changed files', 'Deleted files'] as const).map((fileState) => {
            const treeFiles = files.filter((f) =>
              fileState === 'Deleted files' ? f.status === 'DELETED' : f.status !== 'DELETED',
            );
            if (!treeFiles.length) return null;
            return (
              <section key={fileState} aria-label={fileState}>
                {fileState === 'Deleted files' && <h4>Deleted files</h4>}
                <RepositoryFiles
                  key={JSON.stringify(treeFiles.map((f) => [f.path, f.status, f.unassigned]))}
                  files={treeFiles}
                  repositoryID={repository.repository_id}
                  revisionID={source.revision_id}
                  file={file}
                  choose={choose}
                />
              </section>
            );
          })}
        </section>
      );
    });
  }
  return (
    <div className="source-tree">
      <section aria-label="Unassigned Changes">
        <h3>
          Unassigned Changes<span className="count">{source.unassigned_count}</span>
        </h3>
        {!source.assignments_available ? (
          <p className="notice">
            Topic history is unavailable for this revision. Empty topic sets do not mean these files
            were unassigned.
          </p>
        ) : (
          !source.unassigned_count && <p className="muted">Every file is linked to a topic.</p>
        )}
        {source.assignments_available && renderRepositories(true)}
      </section>
      <section aria-label="All repositories">
        <h3>All repositories</h3>
        {renderRepositories(false)}
      </section>
    </div>
  );
}

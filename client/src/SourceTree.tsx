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
const gitStatus: Record<Model['FileStatus'], GitStatus> = {
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
  const renderRowDecoration = useStableCallback<FileTreeRowDecorationRenderer>(({ item }) => {
    const f = files.find((f) => f.path === item.path);
    if (!f) return null;
    const labels = [];
    if (f.status === 'TYPE_CHANGED') labels.push('Type changed');
    if (f.unassigned) labels.push('Unassigned');
    return labels.length
      ? { text: labels.join(' · '), title: labels.join(' · ') + ' · ' + f.status }
      : null;
  });
  const onSelectionChange = useStableCallback<FileTreeSelectionChangeListener>((paths) => {
    const path = paths.at(-1);
    if (!path || !files.some((f) => f.path === path)) return;
    if (file?.revisionID === revisionID && file.repositoryID === repositoryID && file.path === path)
      return;
    choose({ revisionID, repositoryID, path });
  });
  const { model } = useFileTree({
    paths: files.map((f) => f.path),
    initialExpansion: 'open',
    flattenEmptyDirectories: false,
    search: true,
    renaming: false,
    dragAndDrop: false,
    gitStatus: files.map((f) => ({ path: f.path, status: gitStatus[f.status] })),
    renderRowDecoration,
    onSelectionChange,
  });
  const height = useFileTreeSelector(
    model,
    (tree) => Math.max(1, tree.getVisibleCount()) * tree.getItemHeight() + 48,
  );
  const path =
    file?.revisionID === revisionID && file.repositoryID === repositoryID ? file.path : null;
  useEffect(() => {
    for (const selected of model.getSelectedPaths()) {
      if (selected !== path) model.getItem(selected)?.deselect();
    }
    const item = path ? model.getItem(path) : null;
    if (item) {
      item.select();
      model.scrollToPath(item.getPath(), { focus: false });
    }
  }, [model, path]);
  return (
    <FileTree
      model={model}
      className="repository-files"
      style={{ height: Math.min(height, 320) }}
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
  return (
    <div className="source-tree">
      {(['Unassigned Changes', 'All repositories'] as const).map((group) => (
        <section key={group} aria-label={group}>
          <h3>
            {group}
            {group === 'Unassigned Changes' && (
              <span className="count">{source.unassigned_count}</span>
            )}
          </h3>
          {group === 'Unassigned Changes' && !source.unassigned_count && (
            <p className="muted">Every file is linked to a topic.</p>
          )}
          {source.repositories.map((r) => {
            const files =
              group === 'Unassigned Changes' ? r.files.filter((f) => f.unassigned) : r.files;
            if (group === 'Unassigned Changes' && !files.length) return null;
            return (
              <section key={r.repository_id} aria-label={r.repository_label}>
                <h3>
                  {r.repository_label} <span className="count">{files.length}</span>
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
                        repositoryID={r.repository_id}
                        revisionID={source.revision_id}
                        file={file}
                        choose={choose}
                      />
                    </section>
                  );
                })}
              </section>
            );
          })}
        </section>
      ))}
    </div>
  );
}

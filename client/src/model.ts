import { serverURL, type AnchorInput, type CodeRangeInput, type Model } from './api/client';
export type View = Model['Representation']['type'];
export type TopicOrder = 'manual' | 'attention' | 'intent';
export type Perspective = 'Engineering' | 'Design' | 'Source';
export type Navigation = 'Topics' | 'Findings' | 'All Source Changes';
export type FileSelection = {
  revisionID: string;
  repositoryID: string;
  path: string;
  range?: CodeRangeInput;
};
export type DiscussionTarget = { revisionID: string; anchor: AnchorInput; label: string };

export const viewNames: Record<View, string> = {
  TEXT: 'Text',
  DIAGRAM: 'Diagram',
  CODE_DIFF: 'Code',
  EVIDENCE_LIST: 'Evidence',
};
export const presets: Record<
  Perspective,
  { views: [View, View]; tab: Navigation; order: TopicOrder }
> = {
  Engineering: { views: ['DIAGRAM', 'CODE_DIFF'], tab: 'Topics', order: 'attention' },
  Design: { views: ['TEXT', 'DIAGRAM'], tab: 'Topics', order: 'intent' },
  Source: { views: ['CODE_DIFF', 'EVIDENCE_LIST'], tab: 'All Source Changes', order: 'manual' },
};
export type Preferences = {
  perspective: Perspective;
  views: [View, View];
  split: boolean;
  server: string;
};
export const defaults: Preferences = {
  perspective: 'Engineering',
  views: presets.Engineering.views,
  split: true,
  server: 'http://127.0.0.1:7390',
};
export function loadPreferences(): Preferences {
  try {
    const saved = JSON.parse(localStorage.getItem('prunelle.preferences') || 'null');
    if (
      saved &&
      Object.hasOwn(presets, saved.perspective) &&
      Array.isArray(saved.views) &&
      saved.views.length === 2 &&
      saved.views.every((v: string) => Object.hasOwn(viewNames, v)) &&
      typeof saved.split === 'boolean' &&
      typeof saved.server === 'string'
    )
      return { ...saved, server: serverURL(saved.server) };
  } catch {
    /* Storage can be unavailable in a webview. */
  }
  return defaults;
}

export function attentionFor(topic: Model['Topic'], attention: Model['AttentionSignal'][]) {
  return attention.filter(
    (a) =>
      (a.target.type === 'TOPIC' && a.target.id === topic.id) ||
      (a.target.type === 'FINDING' && topic.finding_ids.includes(a.target.id)) ||
      (a.target.type === 'CHANGE' && topic.change_ids.includes(a.target.id)),
  );
}

export function orderedTopics(
  topics: Model['Topic'][],
  attention: Model['AttentionSignal'][],
  order: TopicOrder,
  parent: string | null = null,
): Model['Topic'][] {
  const priority: Record<string, number> = { HIGH: 3, MEDIUM: 2, LOW: 1, UNKNOWN: 0 };
  function score(topic: Model['Topic']) {
    switch (order) {
      case 'intent':
        return topic.intent_ids.length;
      case 'attention':
        return Math.max(0, ...attentionFor(topic, attention).map((a) => priority[a.priority]));
      default:
        return 0;
    }
  }
  return topics
    .filter((t) => t.parent_id === parent)
    .sort((a, b) => score(b) - score(a) || a.position - b.position);
}

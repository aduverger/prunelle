import type { Model } from '../src/api/client';

export const pathSource: Model['CreateReviewRequest'] = { path: '/source' };
export const repositorySource: Model['CreateReviewRequest'] = {
  repositories: [{ label: 'api', path: '/source/api' }],
};

// @ts-expect-error Creation needs a source, not just a title.
export const missingSource: Model['CreateReviewRequest'] = { title: 'Review' };

// @ts-expect-error A path and an inline source set cannot both select the source.
export const ambiguousSource: Model['CreateReviewRequest'] = {
  path: '/source',
  repositories: [{ label: 'api', path: '/source/api' }],
};

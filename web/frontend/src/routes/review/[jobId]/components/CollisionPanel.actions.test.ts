import { cleanup, render, waitFor } from '@testing-library/svelte';
import { QueryClient } from '@tanstack/svelte-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import QueryClientWrapper from '$lib/components/QueryClientWrapper.svelte';
import type { CreditCollision } from '$lib/api/types';

vi.mock('$lib/api/client', () => ({
	apiClient: {
		listCollisions: vi.fn(),
		resolveCollision: vi.fn(),
	},
}));

import CollisionPanel from './CollisionPanel.svelte';
const { apiClient } = await import('$lib/api/client');
const listCollisions = vi.mocked(apiClient.listCollisions);

function collision(overrides: Partial<CreditCollision>): CreditCollision {
	return {
		id: 1,
		credit_id: 1,
		movie_content_id: 'movie-1',
		field: 'identity_link',
		reported_value: 'Candidate Name',
		canonical_value: 'Candidate Name',
		status: 'open',
		occurrences: 1,
		allowed_resolutions: ['adopt_canonical', 'reassign'],
		...overrides,
	};
}

function renderPanel(rows: CreditCollision[]) {
	listCollisions.mockResolvedValue({ collisions: rows });
	return render(
		CollisionPanel,
		{ movieContentId: 'movie-1' },
		{
			wrapper: QueryClientWrapper,
			wrapperProps: { client: new QueryClient({ defaultOptions: { queries: { retry: false } } }) },
		},
	);
}

afterEach(() => cleanup());
beforeEach(() => vi.clearAllMocks());

describe('CollisionPanel server-derived actions', () => {
	it('does not render backend-invalid actions for an unverified identity link', async () => {
		const view = renderPanel([collision({})]);
		await waitFor(() => expect(view.getByRole('button', { name: /Adopt as truth/ })).toBeTruthy());
		expect(view.getByRole('button', { name: 'Relink' })).toBeTruthy();
		expect(view.queryByRole('button', { name: /Keep catalog/ })).toBeNull();
		expect(view.queryByRole('button', { name: 'Alias' })).toBeNull();
	});

	it('keeps every server-authorized credited-name action reachable', async () => {
		const view = renderPanel([
			collision({
				field: 'credited_name',
				allowed_resolutions: ['keep_identity', 'adopt_canonical', 'adopt_alias', 'reassign'],
			}),
		]);
		for (const name of [/Keep catalog/, /Adopt as truth/, 'Alias', 'Relink']) {
			await waitFor(() => expect(view.getByRole('button', { name })).toBeTruthy());
		}
	});
});

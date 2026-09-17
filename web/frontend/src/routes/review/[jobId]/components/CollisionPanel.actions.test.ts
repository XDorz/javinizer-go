import { cleanup, fireEvent, render, waitFor } from '@testing-library/svelte';
import { QueryClient } from '@tanstack/svelte-query';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import QueryClientWrapper from '$lib/components/QueryClientWrapper.svelte';
import type { CreditCollision } from '$lib/api/types';

vi.mock('$lib/api/client', () => ({
	apiClient: {
		listCollisions: vi.fn(),
		resolveCollision: vi.fn(),
		searchActresses: vi.fn(),
	},
}));

import CollisionPanel from './CollisionPanel.svelte';
const { apiClient } = await import('$lib/api/client');
const listCollisions = vi.mocked(apiClient.listCollisions);
const resolveCollision = vi.mocked(apiClient.resolveCollision);
const searchActresses = vi.mocked(apiClient.searchActresses);

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
		current_actress_id: 7,
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

	it('chooses a searched verified target and posts its visible ID', async () => {
		searchActresses.mockResolvedValue([
			{ id: 7, first_name: 'Current', last_name: 'Actress', verified: true },
			{ id: 8, first_name: 'Verified', last_name: 'Target', verified: true },
			{ id: 9, first_name: 'Hidden', last_name: 'Candidate', verified: false },
		]);
		resolveCollision.mockResolvedValue({ resolved: true, remaining_open: 0 });
		const promptSpy = vi.spyOn(window, 'prompt');
		const view = renderPanel([collision({})]);
		await fireEvent.click(await view.findByRole('button', { name: 'Relink' }));
		await waitFor(() => expect(searchActresses).toHaveBeenCalledWith(''));
		expect(view.queryByRole('button', { name: /Current Actress.*#7/ })).toBeNull();
		expect(view.queryByRole('button', { name: /Hidden Candidate.*#9/ })).toBeNull();
		await fireEvent.click(view.getByRole('button', { name: /Target Verified.*#8/ }));
		expect(view.getByText('Selected target: Target Verified #8')).toBeTruthy();
		await fireEvent.click(view.getByRole('button', { name: 'Confirm relink' }));
		await waitFor(() =>
			expect(resolveCollision).toHaveBeenCalledWith(1, {
				resolution: 'reassign',
				target_actress_id: 8,
			}),
		);
		expect(promptSpy).not.toHaveBeenCalled();
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

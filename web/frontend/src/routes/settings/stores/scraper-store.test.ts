import { describe, expect, it, vi } from 'vitest';
import type { Config } from '$lib/api/types';
import { createScraperStore } from './scraper-store.svelte';

vi.mock('$lib/api/client', () => ({ apiClient: {} }));
vi.mock('$lib/stores/toast', () => ({ toastStore: {} }));
vi.mock('$lib/stores/dialog.svelte', () => ({ confirmDialog: vi.fn() }));
vi.mock('$lib/paraglide/messages', () => ({}));

describe('DLGetchu output ID prefix', () => {
	it('inherits when absent but preserves an explicit empty prefix after editing and reloading', () => {
		let config = { scrapers: { dlgetchu: { enabled: true } } } as Config;
		const createStore = () => {
			const store = createScraperStore({
				getConfig: () => config,
				setConfig: (value) => { config = value!; },
				getProxyProfileNames: () => [],
				refreshLocalProxyProfileChoices: (scrapers) => scrapers,
			});
			store.scrapers = [{
				name: 'dlgetchu', enabled: true, displayName: 'DLGetchu', expanded: true,
				options: [{ key: 'id_prefix', label: 'Output ID prefix', description: '', type: 'string', default: 'getchu-' }],
			}];
			return store;
		};
		let store = createStore();
		expect(store.getOptionValue('dlgetchu', 'id_prefix')).toBe('getchu-');
		store.setOptionValue('dlgetchu', 'id_prefix', 'custom_');
		expect(store.getOptionValue('dlgetchu', 'id_prefix')).toBe('custom_');
		store.setOptionValue('dlgetchu', 'id_prefix', '');
		expect(store.getOptionValue('dlgetchu', 'id_prefix')).toBe('');
		config = JSON.parse(JSON.stringify(config));
		store = createStore();
		expect(store.getOptionValue('dlgetchu', 'id_prefix')).toBe('');
	});
});

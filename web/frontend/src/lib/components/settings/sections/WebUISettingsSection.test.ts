import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render } from '@testing-library/svelte';
import type { SettingsConfig } from '$lib/api/types';
import WebUISettingsSection from './WebUISettingsSection.svelte';
import * as m from '$lib/paraglide/messages';

const originalAnimate = Object.getOwnPropertyDescriptor(Element.prototype, 'animate');

afterEach(() => {
	cleanup();
	if (originalAnimate) Object.defineProperty(Element.prototype, 'animate', originalAnimate);
	else Reflect.deleteProperty(Element.prototype, 'animate');
});

describe('Web UI default file operation', () => {
	it('exposes all operations and updates only the saved Web UI preference', async () => {
		// jsdom has no Web Animations API; settings sections animate when expanded.
		Object.defineProperty(Element.prototype, 'animate', {
			configurable: true,
			value: () => ({
				finished: Promise.resolve(),
				effect: null,
				currentTime: 0,
				cancel() {},
			}),
		});
		const config = {
			webui: { default_file_operation: 'hardlink' },
			output: { operation_mode: 'organize' },
		} as unknown as SettingsConfig;
		const view = render(WebUISettingsSection, { config, inputClass: '', selectClass: '' });
		await fireEvent.click(view.getByRole('button', { name: new RegExp(m.settings_webui_title()) }));
		const select = view.getByLabelText(m.settings_default_file_operation()) as HTMLSelectElement;
		expect(select.value).toBe('hardlink');
		expect(Array.from(select.options, (option) => option.value)).toEqual([
			'move',
			'copy',
			'hardlink',
			'softlink',
		]);
		await fireEvent.change(select, { target: { value: 'softlink' } });
		expect(config.webui?.default_file_operation).toBe('softlink');
		expect(config.output.operation_mode).toBe('organize');
	});
});

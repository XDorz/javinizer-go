import { afterEach, describe, expect, it } from 'vitest';
import { cleanup, fireEvent, render } from '@testing-library/svelte';
import FileOperationHarness from './FileOperationHarness.svelte';

afterEach(cleanup);

describe('review file operation selection', () => {
	it('waits for asynchronous config before committing a default, then ignores refetches', async () => {
		const view = render(FileOperationHarness);
		const select = view.getByRole('combobox') as HTMLSelectElement;
		expect(view.getByTestId('ready').textContent).toBe('false');
		await view.rerender({ config: { default_file_operation: 'hardlink' } });
		expect(select.value).toBe('hardlink');
		expect(view.getByTestId('ready').textContent).toBe('true');
		await view.rerender({ config: { default_file_operation: 'copy' } });
		expect(select.value).toBe('hardlink');
	});

	it('keeps a choice made before config arrives, including an explicit move', async () => {
		const view = render(FileOperationHarness);
		const select = view.getByRole('combobox') as HTMLSelectElement;
		await fireEvent.change(select, { target: { value: 'copy' } });
		await fireEvent.change(select, { target: { value: 'move' } });
		await view.rerender({ config: { default_file_operation: 'hardlink' } });
		expect(select.value).toBe('move');
	});

	it('restores the recorded operation ahead of config and keeps subsequent user edits', async () => {
		const view = render(FileOperationHarness, { restored: 'softlink' });
		const select = view.getByRole('combobox') as HTMLSelectElement;
		expect(select.value).toBe('softlink');
		await view.rerender({ config: { default_file_operation: 'hardlink' } });
		expect(select.value).toBe('softlink');
		await fireEvent.change(select, { target: { value: 'copy' } });
		await view.rerender({ config: { default_file_operation: 'move' } });
		expect(select.value).toBe('copy');
	});

	it('uses the latest default for a new job without carrying the previous temporary choice', async () => {
		const view = render(FileOperationHarness, { config: { default_file_operation: 'hardlink' } });
		const select = view.getByRole('combobox') as HTMLSelectElement;
		await fireEvent.change(select, { target: { value: 'softlink' } });
		await view.rerender({ config: { default_file_operation: 'copy' } });
		await view.rerender({ jobId: 'job-2' });
		expect(select.value).toBe('copy');
		await view.rerender({ jobId: 'job-3', restored: 'move' });
		expect(select.value).toBe('move');
	});

	it('keeps the move default for older servers with no preference', () => {
		const view = render(FileOperationHarness, { config: {} });
		expect((view.getByRole('combobox') as HTMLSelectElement).value).toBe('move');
		expect(view.getByTestId('ready').textContent).toBe('true');
	});
});

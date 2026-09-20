import { untrack } from 'svelte';
import type { FileOperation } from '$lib/api/types';

/** A review owns its selection once configuration arrives or a choice is restored/made. */
export function createFileOperationSelection(
	getJobId: () => string,
	getConfiguredDefault: () => FileOperation | undefined,
	getRestoredOperation: () => FileOperation | undefined,
) {
	let selection = $state<{ jobId: string; operation: FileOperation } | null>(null);

	$effect(() => {
		const jobId = getJobId();
		const configured = getConfiguredDefault();
		if (selection?.jobId === jobId) return;
		const restored = untrack(getRestoredOperation);
		const operation = restored ?? configured;
		// Undefined means config is still loading. Do not commit the move fallback yet.
		if (operation !== undefined) selection = { jobId, operation };
	});

	return {
		get value(): FileOperation {
			return selection?.jobId === getJobId() ? selection.operation : 'move';
		},
		get initialized() {
			return selection?.jobId === getJobId();
		},
		select(operation: FileOperation) {
			selection = { jobId: getJobId(), operation };
		},
	};
}

<script lang="ts">
	import type { FileOperation, WebUIConfig } from '$lib/api/types';
	import { createFileOperationSelection } from './file-operation.svelte';

	let { jobId = 'job-1', config, restored }: {
		jobId?: string;
		config?: WebUIConfig;
		restored?: FileOperation;
	} = $props();
	const selection = createFileOperationSelection(
		() => jobId,
		() => config ? (config.default_file_operation || 'move') : undefined,
		() => restored,
	);
</script>

<output data-testid="ready">{selection.initialized}</output>
<select aria-label="Operation" value={selection.value} onchange={(e) => selection.select(e.currentTarget.value as FileOperation)}>
	<option value="move">Move</option>
	<option value="copy">Copy</option>
	<option value="hardlink">Hard link</option>
	<option value="softlink">Soft link</option>
</select>

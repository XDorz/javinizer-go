import type { FileOperation } from '$lib/api/types';

/** Shared wire mapping for preview, apply, and retries. */
export function fileOperationOptions(operation: FileOperation): {
	copy_only: boolean;
	link_mode?: 'hard' | 'soft';
} {
	return {
		copy_only: operation !== 'move',
		link_mode: operation === 'hardlink' ? 'hard' : operation === 'softlink' ? 'soft' : undefined,
	};
}

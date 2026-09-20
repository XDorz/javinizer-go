<script lang="ts">
	import SettingsSection from '$lib/components/settings/SettingsSection.svelte';
	import type { FileOperation, SettingsConfig } from '$lib/api/types';
	import * as m from '$lib/paraglide/messages';
	import { SUPPORTED_LOCALES } from '$lib/i18n/locale';

	interface Props {
		config: SettingsConfig;
		inputClass: string;
	selectClass: string;
	}

	let { config, inputClass, selectClass }: Props = $props();

	function getSelectedView(): string {
		return config.webui?.default_review_view || 'grid-poster';
	}

	function setSelectedView(value: string) {
		if (!config.webui) config.webui = {};
		config.webui.default_review_view = value;
	}

	function setFileOperation(value: FileOperation) {
		if (!config.webui) config.webui = {};
		config.webui.default_file_operation = value;
	}

	function getSelectedLanguage(): string {
		return config.ui?.language || 'auto';
	}

	function setSelectedLanguage(value: string) {
		if (!config.ui) config.ui = { language: 'auto' };
		config.ui.language = value;
	}

	function handleLanguageChange(value: string) {
		// Only mark the config dirty here. Applying immediately would reload the
		// page and discard the unsaved change; the saved language is applied
		// (with a reload if the rendered locale changed) after a successful save
		// via applySavedLocale in the settings store.
		setSelectedLanguage(value);
	}
</script>

<SettingsSection title={m.settings_webui_title()} description={m.settings_webui_desc()} defaultExpanded={false}>
	<div class="space-y-4">
		<div>
			<label class="block text-sm font-medium mb-2" for="webui-default-review-view">{m.settings_default_review_view()}</label>
			<select
				id="webui-default-review-view"
				value={getSelectedView()}
				onchange={(e) => setSelectedView((e.target as HTMLSelectElement).value)}
				class={selectClass}
			>
				<option value="grid-poster">{m.settings_review_view_grid_poster()}</option>
				<option value="grid-cover">{m.settings_review_view_grid_cover()}</option>
				<option value="detail">{m.settings_review_view_detail()}</option>
			</select>
			<p class="text-xs text-muted-foreground mt-1">
				{m.settings_default_review_view_desc()}
			</p>
		</div>
		<div>
			<label class="block text-sm font-medium mb-2" for="webui-default-file-operation">{m.settings_default_file_operation()}</label>
			<select
				id="webui-default-file-operation"
				value={config.webui?.default_file_operation || 'move'}
				onchange={(e) => setFileOperation((e.target as HTMLSelectElement).value as FileOperation)}
				class={selectClass}
			>
				<option value="move">{m.review_op_move()}</option>
				<option value="copy">{m.review_op_copy()}</option>
				<option value="hardlink">{m.review_op_hardlink()}</option>
				<option value="softlink">{m.review_op_softlink()}</option>
			</select>
			<p class="text-xs text-muted-foreground mt-1">{m.settings_default_file_operation_desc()}</p>
		</div>
		<div>
			<label class="block text-sm font-medium mb-2" for="webui-language">{m.settings_language()}</label>
			<select
				id="webui-language"
				value={getSelectedLanguage()}
				onchange={(e) => void handleLanguageChange((e.target as HTMLSelectElement).value)}
				class={selectClass}
			>
				{#each SUPPORTED_LOCALES as locale}
					<option value={locale.tag}>{locale.selfName}</option>
				{/each}
			</select>
			<p class="text-xs text-muted-foreground mt-1">
				{m.settings_language_desc()}
			</p>
		</div>
	</div>
</SettingsSection>

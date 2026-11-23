<script lang="ts">
	import * as AlertDialog from '#lib/components/ui/alert-dialog/index.js';

	type Props = {
		open: boolean;
		title: string;
		description: string;
		cancel: string;
		action: string;
		onconfirm: () => void;
	};
	let { open = $bindable(), title, description, cancel, action, onconfirm }: Props = $props();
</script>

<AlertDialog.Root bind:open>
	<AlertDialog.Content interactOutsideBehavior="close">
		<AlertDialog.Header>
			<AlertDialog.Title>{title}</AlertDialog.Title>
			<AlertDialog.Description>{description}</AlertDialog.Description>
		</AlertDialog.Header>
		<AlertDialog.Footer>
			<AlertDialog.Cancel>{cancel}</AlertDialog.Cancel>
			<AlertDialog.Action variant="destructive" onclick={onconfirm}>{action}</AlertDialog.Action>
		</AlertDialog.Footer>
	</AlertDialog.Content>
</AlertDialog.Root>

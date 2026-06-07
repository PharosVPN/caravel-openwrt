// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 The PharosVPN Authors
//
// PharosVPN client — Profiles view. Lists stored profiles, uploads a .pharos
// (browser file → rpcd → `caravel-owrt import`), runs account-mode `sync`, and
// removes a profile. All crypto stays in the Go binary behind the rpcd object;
// this view only moves bytes and toggles.
'use strict';
'require view';
'require ui';
'require rpc';
'require poll';

var callList = rpc.declare({
	object: 'pharos',
	method: 'list',
	expect: { profiles: [] }
});

var callImport = rpc.declare({
	object: 'pharos',
	method: 'import',
	params: [ 'name', 'data' ]
});

var callSync = rpc.declare({
	object: 'pharos',
	method: 'sync',
	params: [ 'idfile', 'email', 'password', 'name' ]
});

var callRemove = rpc.declare({
	object: 'pharos',
	method: 'remove',
	params: [ 'name' ]
});

// readFile resolves a browser File to its text content.
function readFile(file) {
	return new Promise(function(resolve, reject) {
		var r = new FileReader();
		r.onload = function() { resolve(r.result); };
		r.onerror = function() { reject(new Error('could not read file')); };
		r.readAsText(file);
	});
}

return view.extend({
	load: function() {
		return callList();
	},

	// renderTable (re)draws the stored-profiles table into #pharos-profiles.
	renderTable: function(profiles) {
		var rows = (profiles || []).map(L.bind(function(p) {
			return [
				p.name,
				p.enc,
				p.synced ? _('account (sync)') : _('file'),
				E('button', {
					'class': 'btn cbi-button cbi-button-remove',
					'click': ui.createHandlerFn(this, function(name) {
						if (!confirm(_('Remove profile "%s"?').format(name)))
							return;
						return callRemove(name).then(L.bind(function(res) {
							if (res && res.error)
								ui.addNotification(null, E('p', res.error), 'danger');
							else
								ui.addNotification(null, E('p', _('Removed profile "%s".').format(name)), 'info');
							return this.refresh();
						}, this));
					}, this, p.name)
				}, _('Remove'))
			];
		}, this));

		var tbl = E('table', { 'class': 'table cbi-section-table', 'id': 'pharos-profiles' }, [
			E('tr', { 'class': 'tr table-titles' }, [
				E('th', { 'class': 'th' }, _('Name')),
				E('th', { 'class': 'th' }, _('Encryption')),
				E('th', { 'class': 'th' }, _('Source')),
				E('th', { 'class': 'th' }, _('Actions'))
			])
		]);
		cbi_update_table(tbl, rows, E('em', _('No profiles stored. Upload a .pharos below.')));
		return tbl;
	},

	refresh: function() {
		return callList().then(L.bind(function(profiles) {
			var old = document.getElementById('pharos-profiles');
			if (old)
				old.parentNode.replaceChild(this.renderTable(profiles), old);
		}, this));
	},

	render: function(profiles) {
		var self = this;

		// ── Upload a .pharos ───────────────────────────────────────────────
		var fileInput = E('input', { 'type': 'file', 'accept': '.pharos,application/json' });
		var nameInput = E('input', { 'type': 'text', 'class': 'cbi-input-text', 'placeholder': _('optional name') });

		var uploadBtn = E('button', {
			'class': 'btn cbi-button cbi-button-action important',
			'click': ui.createHandlerFn(self, function() {
				var file = fileInput.files && fileInput.files[0];
				if (!file)
					return ui.addNotification(null, E('p', _('Choose a .pharos file first.')), 'warning');
				return readFile(file).then(function(text) {
					var name = nameInput.value || file.name.replace(/\.pharos$/, '');
					return callImport(name, text).then(function(res) {
						if (res && res.error) {
							ui.addNotification(null, E('p', res.error), 'danger');
						} else {
							ui.addNotification(null, E('p', _('Imported profile "%s".').format(name)), 'info');
							fileInput.value = '';
							nameInput.value = '';
						}
						return self.refresh();
					});
				}).catch(function(e) {
					ui.addNotification(null, E('p', e.message), 'danger');
				});
			})
		}, _('Upload & import'));

		var uploadSection = E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, _('Upload a profile (.pharos)')),
			E('p', { 'class': 'cbi-section-descr' },
				_('Import a profile file exported by the PharosVPN controller. The blob is stored at /etc/pharos/profiles (0600) — never in the world-readable config.')),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Profile file')),
				E('div', { 'class': 'cbi-value-field' }, [ fileInput ])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Store as')),
				E('div', { 'class': 'cbi-value-field' }, [ nameInput ])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('div', { 'class': 'cbi-value-field' }, [ uploadBtn ])
			])
		]);

		// ── Account sync ───────────────────────────────────────────────────
		var idInput = E('input', { 'type': 'file', 'accept': '.pharosid,application/json' });
		var emailInput = E('input', { 'type': 'text', 'class': 'cbi-input-text', 'placeholder': _('email (optional)') });
		var pwInput = E('input', { 'type': 'password', 'class': 'cbi-input-password', 'placeholder': _('account passphrase') });

		var syncBtn = E('button', {
			'class': 'btn cbi-button cbi-button-action',
			'click': ui.createHandlerFn(self, function() {
				var file = idInput.files && idInput.files[0];
				if (!file)
					return ui.addNotification(null, E('p', _('Choose a .pharosid bundle first.')), 'warning');
				if (!pwInput.value)
					return ui.addNotification(null, E('p', _('The account passphrase is required.')), 'warning');
				return readFile(file).then(function(text) {
					return callSync(text, emailInput.value || '', pwInput.value, '').then(function(res) {
						pwInput.value = '';
						if (res && res.error) {
							ui.addNotification(null, E('p', res.error), 'danger');
						} else {
							ui.addNotification(null, E('p', res.message || _('Profile synced.')), 'info');
							idInput.value = '';
						}
						return self.refresh();
					});
				}).catch(function(e) {
					ui.addNotification(null, E('p', e.message), 'danger');
				});
			})
		}, _('Sync now'));

		var syncSection = E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, _('Account sync (.pharosid)')),
			E('p', { 'class': 'cbi-section-descr' },
				_('Fetch your account profile from the controller using a .pharosid bundle and your passphrase. The passphrase is piped to the client on stdin and never stored.')),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('.pharosid bundle')),
				E('div', { 'class': 'cbi-value-field' }, [ idInput ])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Email')),
				E('div', { 'class': 'cbi-value-field' }, [ emailInput ])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Passphrase')),
				E('div', { 'class': 'cbi-value-field' }, [ pwInput ])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('div', { 'class': 'cbi-value-field' }, [ syncBtn ])
			])
		]);

		// Refresh the list periodically (a CLI `import`/`sync` from the shell
		// shows up without a reload).
		poll.add(L.bind(this.refresh, this), 10);

		return E('div', {}, [
			E('h2', {}, _('PharosVPN — Profiles')),
			E('div', { 'class': 'cbi-section' }, [
				E('h3', {}, _('Stored profiles')),
				this.renderTable(profiles)
			]),
			uploadSection,
			syncSection
		]);
	},

	handleSaveApply: null,
	handleSave: null,
	handleReset: null
});

// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 The PharosVPN Authors
//
// PharosVPN client — Connection view. Pick a profile (and, if it carries
// several, a connection/node/exit), choose full- vs split-tunnel and the
// kill-switch, Connect / Disconnect, and watch live status (up/down, endpoint,
// rx/tx) polled from the rpcd `status` method. The selection + toggles persist
// to /etc/config/pharosvpn and the procd service brings the tunnel up; the Go
// worker does the tun + routing.
'use strict';
'require view';
'require ui';
'require rpc';
'require poll';

var callList = rpc.declare({ object: 'pharos', method: 'list', expect: { profiles: [] } });
var callInspect = rpc.declare({ object: 'pharos', method: 'inspect', params: [ 'profile', 'password' ] });
var callStatus = rpc.declare({ object: 'pharos', method: 'status' });
var callGetConfig = rpc.declare({ object: 'pharos', method: 'getconfig' });
var callConnect = rpc.declare({ object: 'pharos', method: 'connect', params: [ 'profile', 'connection', 'node', 'full_tunnel', 'killswitch' ] });
var callDisconnect = rpc.declare({ object: 'pharos', method: 'disconnect' });

function humanBytes(n) {
	n = n || 0;
	var u = [ 'B', 'KB', 'MB', 'GB', 'TB' ], i = 0;
	while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
	return (i === 0 ? n : n.toFixed(1)) + ' ' + u[i];
}

return view.extend({
	// view state
	current: { profile: '', connection: '', node: '', inspected: null },

	load: function() {
		return Promise.all([ callList(), callGetConfig() ]);
	},

	// inspectAndFill loads a profile's connections/nodes and rebuilds the
	// connection + node dropdowns. Password-protected profiles report
	// needs_password; the pickers then stay collapsed (connect prompts).
	inspectAndFill: function(name, password) {
		return callInspect(name, password || '').then(L.bind(function(info) {
			this.current.inspected = info;
			var connSel = document.getElementById('pharos-connection');
			var nodeSel = document.getElementById('pharos-node');
			var hint = document.getElementById('pharos-pw-hint');
			if (!connSel) return;

			connSel.innerHTML = '';
			nodeSel.innerHTML = '';

			if (info && info.needs_password) {
				hint.style.display = '';
				connSel.appendChild(E('option', { 'value': '' }, _('(password-protected — enter at connect)')));
				connSel.disabled = true;
				nodeSel.disabled = true;
				return;
			}
			if (info && info.error) {
				hint.style.display = 'none';
				ui.addNotification(null, E('p', info.error), 'danger');
				return;
			}
			hint.style.display = 'none';
			connSel.disabled = false;
			nodeSel.disabled = false;

			var profs = (info && info.profiles) || [];
			if (profs.length === 0) {
				connSel.appendChild(E('option', { 'value': '' }, _('(no connection in profile)')));
				return;
			}
			profs.forEach(function(p) {
				connSel.appendChild(E('option', { 'value': p.name }, '%s [%s]'.format(p.name, p.protocol)));
			});
			connSel.value = this.current.connection || profs[0].name;
			this.fillNodes();
		}, this));
	},

	// fillNodes populates the node/exit dropdown for the chosen connection.
	fillNodes: function() {
		var info = this.current.inspected;
		var connSel = document.getElementById('pharos-connection');
		var nodeSel = document.getElementById('pharos-node');
		if (!info || !nodeSel) return;
		nodeSel.innerHTML = '';
		var prof = (info.profiles || []).filter(function(p) { return p.name === connSel.value; })[0];
		if (!prof) return;

		nodeSel.appendChild(E('option', { 'value': '' }, _('Automatic (entry/first)')));
		(prof.nodes || []).forEach(function(n) {
			var label = n.region ? '%s (%s)'.format(n.name, n.region) : n.name;
			nodeSel.appendChild(E('option', { 'value': n.id }, label));
		});
		// A cascade profile shows its egress path read-only so the user sees the hops.
		var pathInfo = document.getElementById('pharos-path');
		if (pathInfo) {
			if (prof.path && prof.path.hops && prof.path.hops.length) {
				pathInfo.textContent = _('Egress path: ') + prof.path.hops.map(function(h) { return h.name; }).join(' → ');
				pathInfo.style.display = '';
			} else {
				pathInfo.style.display = 'none';
			}
		}
		nodeSel.value = this.current.node || '';
	},

	renderStatus: function(st) {
		var up = st && st.up;
		var badge = E('span', {
			'class': up ? 'label' : 'label',
			'style': 'padding:4px 8px;border-radius:4px;color:#fff;background:' + (up ? '#46a546' : '#999')
		}, up ? _('Connected') : _('Disconnected'));

		var rows = [];
		if (up) {
			rows.push([ _('Profile'), st.profile || '—' ]);
			rows.push([ _('Interface'), st.iface || '—' ]);
			rows.push([ _('Endpoint dialed'), st.endpoint || '—' ]);
			rows.push([ _('Received'), humanBytes(st.rx) ]);
			rows.push([ _('Sent'), humanBytes(st.tx) ]);
			if (st.since)
				rows.push([ _('Since'), st.since ]);
		} else if (st && st.error) {
			rows.push([ _('Note'), st.error ]);
		}

		return E('div', {}, [
			E('div', { 'style': 'margin-bottom:10px' }, [ E('strong', {}, _('Status: ')), badge ]),
			E('table', { 'class': 'table' }, rows.map(function(r) {
				return E('tr', { 'class': 'tr' }, [
					E('td', { 'class': 'td', 'style': 'width:30%' }, r[0]),
					E('td', { 'class': 'td' }, r[1])
				]);
			}))
		]);
	},

	refreshStatus: function() {
		return callStatus().then(L.bind(function(st) {
			var box = document.getElementById('pharos-status');
			if (box)
				box.parentNode.replaceChild(E('div', { 'id': 'pharos-status' }, this.renderStatus(st)), box);
		}, this));
	},

	render: function(data) {
		var self = this;
		var profiles = data[0] || [];
		var cfg = data[1] || {};
		this.current.profile = cfg.profile || (profiles[0] && profiles[0].name) || '';
		this.current.connection = cfg.connection || '';
		this.current.node = cfg.node || '';

		// ── profile picker ─────────────────────────────────────────────────
		var profSel = E('select', { 'class': 'cbi-input-select', 'id': 'pharos-profile' },
			profiles.length
				? profiles.map(function(p) { return E('option', { 'value': p.name }, '%s [%s]'.format(p.name, p.enc)); })
				: [ E('option', { 'value': '' }, _('(no profiles — import one first)')) ]);
		profSel.value = this.current.profile;
		profSel.addEventListener('change', function() {
			self.current.profile = profSel.value;
			self.current.connection = '';
			self.current.node = '';
			self.inspectAndFill(profSel.value, pwInput.value);
		});

		var connSel = E('select', { 'class': 'cbi-input-select', 'id': 'pharos-connection' });
		connSel.addEventListener('change', function() {
			self.current.connection = connSel.value;
			self.fillNodes();
		});

		var nodeSel = E('select', { 'class': 'cbi-input-select', 'id': 'pharos-node' });

		var pwInput = E('input', { 'type': 'password', 'class': 'cbi-input-password', 'placeholder': _('only if password-protected') });
		var pwHint = E('div', { 'id': 'pharos-pw-hint', 'class': 'cbi-value-description', 'style': 'display:none;color:#b58900' },
			_('This profile is password-protected. Enter the password to preview nodes, or just connect.'));

		var fullToggle = E('input', { 'type': 'checkbox', 'id': 'pharos-full', 'checked': (cfg.full_tunnel !== false) ? '' : null });
		var ksToggle = E('input', { 'type': 'checkbox', 'id': 'pharos-ks', 'checked': (cfg.killswitch !== false) ? '' : null });

		var pathInfo = E('div', { 'id': 'pharos-path', 'class': 'cbi-value-description', 'style': 'display:none' });

		// ── connect / disconnect ───────────────────────────────────────────
		var connectBtn = E('button', {
			'class': 'btn cbi-button cbi-button-action important',
			'click': ui.createHandlerFn(self, function() {
				if (!profSel.value)
					return ui.addNotification(null, E('p', _('Select a profile first.')), 'warning');
				return callConnect(
					profSel.value,
					connSel.value || '',
					nodeSel.value || '',
					fullToggle.checked,
					ksToggle.checked
				).then(function(res) {
					if (res && res.error)
						ui.addNotification(null, E('p', res.error), 'danger');
					else
						ui.addNotification(null, E('p', _('Connecting "%s"…').format(profSel.value)), 'info');
					return self.refreshStatus();
				});
			})
		}, _('Connect'));

		var disconnectBtn = E('button', {
			'class': 'btn cbi-button cbi-button-remove',
			'click': ui.createHandlerFn(self, function() {
				return callDisconnect().then(function(res) {
					if (res && res.error)
						ui.addNotification(null, E('p', res.error), 'danger');
					else
						ui.addNotification(null, E('p', _('Disconnected.')), 'info');
					return self.refreshStatus();
				});
			})
		}, _('Disconnect'));

		var form = E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, _('Connect')),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Profile')),
				E('div', { 'class': 'cbi-value-field' }, [ profSel ])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Connection')),
				E('div', { 'class': 'cbi-value-field' }, [ connSel ])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Node / exit')),
				E('div', { 'class': 'cbi-value-field' }, [ nodeSel, pathInfo ])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Password')),
				E('div', { 'class': 'cbi-value-field' }, [ pwInput, pwHint ])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Full tunnel')),
				E('div', { 'class': 'cbi-value-field' }, [ fullToggle,
					E('span', { 'class': 'cbi-value-description' }, _(' route all LAN traffic through the tunnel (off = only the profile\'s allowed IPs)')) ])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('label', { 'class': 'cbi-value-title' }, _('Kill-switch')),
				E('div', { 'class': 'cbi-value-field' }, [ ksToggle,
					E('span', { 'class': 'cbi-value-description' }, _(' do not fall back to the clear WAN if the tunnel drops (not yet enforced — see README)')) ])
			]),
			E('div', { 'class': 'cbi-value' }, [
				E('div', { 'class': 'cbi-value-field' }, [ connectBtn, ' ', disconnectBtn ])
			])
		]);

		var statusSection = E('div', { 'class': 'cbi-section' }, [
			E('h3', {}, _('Live status')),
			E('div', { 'id': 'pharos-status' }, this.renderStatus(null))
		]);

		var root = E('div', {}, [
			E('h2', {}, _('PharosVPN — Connection')),
			form,
			statusSection
		]);

		// Initial fill + live status polling.
		this.inspectAndFill(this.current.profile, '');
		this.refreshStatus();
		poll.add(L.bind(this.refreshStatus, this), 3);

		// Re-inspect when a password is typed (so pickers populate).
		pwInput.addEventListener('change', function() {
			self.inspectAndFill(profSel.value, pwInput.value);
		});

		return root;
	},

	handleSaveApply: null,
	handleSave: null,
	handleReset: null
});

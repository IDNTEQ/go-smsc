import { h, render } from 'https://esm.sh/preact@10.19.3';
import { useState, useEffect, useCallback, useRef } from 'https://esm.sh/preact@10.19.3/hooks';
import htm from 'https://esm.sh/htm@3.1.1';

const html = htm.bind(h);

// ---------------------------------------------------------------------------
// API helper
// ---------------------------------------------------------------------------

async function api(method, path, body = null) {
    const jwt = localStorage.getItem('jwt');
    const opts = {
        method,
        headers: { 'Content-Type': 'application/json' },
    };
    if (jwt) opts.headers['Authorization'] = 'Bearer ' + jwt;
    if (body) opts.body = JSON.stringify(body);
    const res = await fetch(path, opts);
    if (res.status === 401) {
        localStorage.removeItem('jwt');
        location.hash = '#/login';
        throw new Error('Unauthorized');
    }
    if (res.status === 204) return null;
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'Request failed');
    return data;
}

// ---------------------------------------------------------------------------
// Toast notification
// ---------------------------------------------------------------------------

let _setToast = null;

function ToastContainer() {
    const [toast, setToast] = useState(null);
    _setToast = setToast;

    useEffect(() => {
        if (toast) {
            const t = setTimeout(() => setToast(null), 3000);
            return () => clearTimeout(t);
        }
    }, [toast]);

    if (!toast) return null;
    return html`<div class="toast ${toast.type || ''}">${toast.message}</div>`;
}

function showToast(message, type = '') {
    if (_setToast) _setToast({ message, type });
}

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

function fmt(n) {
    return (n || 0).toLocaleString();
}

function statusBadge(status) {
    const colors = {
        delivered: 'green', forwarded: 'blue', accepted: 'yellow',
        failed: 'red', rejected: 'red', healthy: 'green', unhealthy: 'red',
    };
    const color = colors[status] || 'gray';
    return html`<span class="status-badge badge-${color}">${status}</span>`;
}

function formatTime(ts) {
    if (!ts) return '';
    const d = new Date(ts);
    const now = new Date();
    if (d.toDateString() === now.toDateString()) {
        return d.toLocaleTimeString();
    }
    return d.toLocaleString();
}

function textToArray(text) {
    return text.split('\n').map(s => s.trim()).filter(Boolean);
}

function arrayToText(arr) {
    return (arr || []).join('\n');
}

// ---------------------------------------------------------------------------
// Login page
// ---------------------------------------------------------------------------

function LoginPage() {
    const [username, setUsername] = useState('');
    const [password, setPassword] = useState('');
    const [error, setError] = useState('');
    const [loading, setLoading] = useState(false);

    const login = async (e) => {
        e.preventDefault();
        setLoading(true);
        setError('');
        try {
            const data = await api('POST', '/admin/api/login', { username, password });
            localStorage.setItem('jwt', data.token);
            location.hash = '#/dashboard';
        } catch (err) {
            setError('Invalid credentials');
        } finally {
            setLoading(false);
        }
    };

    return html`
        <div class="login-container">
            <article>
                <h2>SMSC Gateway Login</h2>
                ${error && html`<p style="color: #ef4444">${error}</p>`}
                <form onSubmit=${login}>
                    <label>Username
                        <input type="text" value=${username}
                               onInput=${e => setUsername(e.target.value)}
                               required autocomplete="username" />
                    </label>
                    <label>Password
                        <input type="password" value=${password}
                               onInput=${e => setPassword(e.target.value)}
                               required autocomplete="current-password" />
                    </label>
                    <button type="submit" disabled=${loading}
                            aria-busy=${loading}>
                        ${loading ? 'Signing in...' : 'Login'}
                    </button>
                </form>
            </article>
        </div>
    `;
}

// ---------------------------------------------------------------------------
// Dashboard page -- real-time WebSocket updates
// ---------------------------------------------------------------------------

function StatCard({ value, label, sublabel, title }) {
    return html`
        <div class="stat-card" title=${title || ''}>
            <div class="value">${fmt(value)}</div>
            <div class="label">${label}</div>
            ${sublabel && html`<div class="sublabel">${sublabel}</div>`}
        </div>
    `;
}

function DashboardPage() {
    const [stats, setStats] = useState(null);
    const [messages, setMessages] = useState([]);

    useEffect(() => {
        // Fetch initial stats via REST
        api('GET', '/admin/api/stats').then(setStats).catch(() => {});
        api('GET', '/admin/api/messages?limit=10').then(d => setMessages(d || [])).catch(() => {});

        // WebSocket for real-time updates
        const jwt = localStorage.getItem('jwt');
        const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
        const ws = new WebSocket(`${proto}//${location.host}/admin/ws`);

        ws.onopen = () => {
            // Send JWT as first message for auth
            if (jwt) ws.send(jwt);
        };

        ws.onmessage = (e) => {
            try {
                setStats(JSON.parse(e.data));
            } catch {}
        };

        ws.onerror = () => {};
        ws.onclose = () => {};

        // Polling fallback: refresh stats every 3 seconds in case WebSocket
        // is not available or disconnects.
        const interval = setInterval(() => {
            api('GET', '/admin/api/stats').then(setStats).catch(() => {});
            api('GET', '/admin/api/messages?limit=10').then(d => setMessages(d || [])).catch(() => {});
        }, 3000);

        return () => { ws.close(); clearInterval(interval); };
    }, []);

    if (!stats) return html`<p aria-busy="true">Loading dashboard...</p>`;

    const pools = stats.pools || [];

    return html`
        <h2>Dashboard</h2>

        <div class="dashboard-grid">
            <${StatCard} value=${stats.connections} label="Connections"
                sublabel="northbound SMPP" title="Active engine connections" />
            <${StatCard} value=${stats.total_submits} label="Submits"
                sublabel="accepted total" title="Total submit_sm messages accepted" />
            <${StatCard} value=${stats.total_dlrs} label="DLRs"
                sublabel="received total" title="Total delivery receipts received" />
            <${StatCard} value=${stats.total_mo} label="MO Messages"
                sublabel="received total" title="Total mobile-originated messages" />
        </div>

        <div class="dashboard-grid">
            <${StatCard} value=${stats.total_forwarded} label="Forwarded"
                sublabel="to downstream SMSC" title="Total submits successfully forwarded" />
            <${StatCard} value=${stats.total_throttled} label="Throttled"
                sublabel="rate limited" title="Total submits rejected by rate limiter" />
            <${StatCard} value=${stats.affinity_size} label="Affinity Table"
                sublabel="MSISDN mappings" title="MSISDN-to-connection affinity entries" />
            <${StatCard} value=${stats.correlation_size} label="Correlations"
                sublabel="pending DLR maps" title="SMSC message ID to gateway ID mappings" />
        </div>

        <div class="dashboard-grid">
            <${StatCard} value=${stats.store_size} label="Store Size"
                sublabel="messages cached" title="Messages persisted in Pebble store" />
            <${StatCard} value=${stats.retry_queue} label="Retry Queue"
                sublabel="DLR/MO pending" title="Northbound deliveries pending retry" />
            <${StatCard} value=${stats.submit_retries} label="Submit Retries"
                sublabel="southbound pending" title="Southbound submits pending retry" />
            <${StatCard} value=${pools.length} label="Binds"
                sublabel="configured" title="Number of outbound SMSC binds" />
        </div>

        <h3>Outbound Bind Health</h3>
        ${pools.length === 0
            ? html`<p>No outbound binds configured.</p>`
            : html`
                <table>
                    <thead>
                        <tr>
                            <th>Name</th>
                            <th>Active Connections</th>
                            <th>Status</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${pools.map(p => html`
                            <tr key=${p.name}>
                                <td><span class="health-dot ${p.healthy ? 'green' : 'red'}"></span>${p.name}</td>
                                <td>${p.active_connections}</td>
                                <td>${statusBadge(p.healthy ? 'healthy' : 'unhealthy')}</td>
                            </tr>
                        `)}
                    </tbody>
                </table>
            `
        }

        <h3>Recent Messages</h3>
        ${messages.length === 0
            ? html`<p>No recent messages.</p>`
            : html`
                <table class="compact-table">
                    <thead>
                        <tr>
                            <th>Time</th>
                            <th>Source / Dest</th>
                            <th>Status</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${messages.map(m => html`
                            <tr key=${m.gw_msg_id}>
                                <td>${formatTime(m.created_at)}</td>
                                <td>${m.source_addr}<span class="message-arrow"> → </span>${m.dest_addr}</td>
                                <td>${statusBadge(m.status)}</td>
                            </tr>
                        `)}
                    </tbody>
                </table>
            `
        }
    `;
}

// ---------------------------------------------------------------------------
// Connections page
// ---------------------------------------------------------------------------

function relativeTime(ts) {
    if (!ts) return '';
    const diff = (Date.now() - new Date(ts).getTime()) / 1000;
    if (diff < 60) return Math.floor(diff) + 's ago';
    if (diff < 3600) return Math.floor(diff / 60) + ' min ago';
    if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
    return Math.floor(diff / 86400) + 'd ago';
}

function ConnectionsPage() {
    const [conns, setConns] = useState([]);
    const [pools, setPools] = useState([]);
    const [loading, setLoading] = useState(true);
    const [selected, setSelected] = useState(null);
    const [detail, setDetail] = useState(null);

    const refresh = useCallback(() => {
        setLoading(true);
        Promise.all([
            api('GET', '/admin/api/connections').catch(() => []),
            api('GET', '/admin/api/pools').catch(() => []),
        ]).then(([c, p]) => {
            setConns(c || []);
            setPools(p || []);
        }).finally(() => setLoading(false));
    }, []);

    useEffect(() => {
        refresh();
        const interval = setInterval(refresh, 5000);
        return () => clearInterval(interval);
    }, []);

    const selectConn = async (id) => {
        setSelected(id);
        try {
            const data = await api('GET', '/admin/api/connections/' + encodeURIComponent(id));
            setDetail(data);
        } catch {
            setDetail(null);
        }
    };

    const backToList = () => {
        setSelected(null);
        setDetail(null);
    };

    if (selected && detail) {
        return html`
            <h2>Connection Detail</h2>
            <p><a href="#" onClick=${(e) => { e.preventDefault(); backToList(); }}>&larr; Back to connections</a></p>
            <div class="form-card">
                <h4>${detail.system_id}</h4>
                <table>
                    <tbody>
                        <tr><td><strong>System ID</strong></td><td>${detail.has_config
                            ? html`<a href="#/connconfigs">${detail.system_id}</a>`
                            : detail.system_id}</td></tr>
                        <tr><td><strong>Remote Address</strong></td><td>${detail.remote_addr}</td></tr>
                        <tr><td><strong>Bind Mode</strong></td><td>${detail.bind_mode || 'transceiver'}</td></tr>
                        <tr><td><strong>Bound Since</strong></td><td>${new Date(detail.bound_since).toLocaleString()} (${relativeTime(detail.bound_since)})</td></tr>
                        <tr><td><strong>Total Submits</strong></td><td>${fmt(detail.total_submits)}</td></tr>
                        <tr><td><strong>Current TPS</strong></td><td>${fmt(detail.current_tps)}</td></tr>
                        <tr><td><strong>In-Flight</strong></td><td>${detail.in_flight}</td></tr>
                        <tr><td><strong>Status</strong></td><td>${statusBadge('healthy')}</td></tr>
                    </tbody>
                </table>

                ${detail.config
                    ? html`
                        <h5 style="margin-top: 1.5rem">Client Config</h5>
                        <table>
                            <tbody>
                                <tr><td><strong>Description</strong></td><td>${detail.config.description || '(none)'}</td></tr>
                                <tr><td><strong>Max TPS</strong></td><td>${detail.config.max_tps || 'unlimited'}</td></tr>
                                <tr><td><strong>Cost / SMS</strong></td><td>${detail.config.cost_per_sms ? detail.config.cost_per_sms.toFixed(2) : '0.00'}</td></tr>
                                <tr><td><strong>Max Binds</strong></td><td>${detail.config.max_binds || 'unlimited'}</td></tr>
                                <tr><td><strong>Allowed Prefixes</strong></td><td>${(detail.config.allowed_prefixes || []).join(', ') || 'all'}</td></tr>
                                <tr><td><strong>Allowed IPs</strong></td><td>${(detail.config.allowed_ips || []).join(', ') || 'all'}</td></tr>
                                <tr><td><strong>Allowed Bind Modes</strong></td><td>${(detail.config.allowed_bind_modes || []).join(', ') || 'all'}</td></tr>
                            </tbody>
                        </table>
                        <a href="#/connconfigs"><button class="secondary" style="margin-top: 0.5rem">View Client Config</button></a>
                    `
                    : html`<p style="color: #888; margin-top: 1rem">Using legacy/test authentication</p>`
                }
            </div>
        `;
    }

    return html`
        <h2>Connections
            <button style="margin-left: 1rem; font-size: 0.8rem; padding: 0.3rem 0.8rem"
                    onClick=${refresh} aria-busy=${loading}>
                Refresh
            </button>
        </h2>

        <h3>Inbound - Upstream (Clients → Gateway)</h3>
        ${conns.length === 0 && !loading
            ? html`<p style="color: var(--pico-muted-color)">No inbound connections.</p>`
            : html`
                <table>
                    <thead>
                        <tr>
                            <th>System ID</th>
                            <th>Remote IP</th>
                            <th>Bind Mode</th>
                            <th>Uptime</th>
                            <th>TPS</th>
                            <th>Submits</th>
                            <th>Status</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${conns.map(c => html`
                            <tr key=${c.id} onClick=${() => selectConn(c.id)}
                                style="cursor: pointer">
                                <td><code>${c.system_id}</code></td>
                                <td>${c.remote_addr}</td>
                                <td>${c.bind_mode || 'transceiver'}</td>
                                <td>${relativeTime(c.bound_since)}</td>
                                <td>${fmt(c.current_tps)}</td>
                                <td>${fmt(c.total_submits)}</td>
                                <td>${statusBadge('healthy')}</td>
                            </tr>
                        `)}
                    </tbody>
                </table>
            `
        }

        <h3 style="margin-top: 2rem">Outbound - Downstream (Gateway → SMSCs)</h3>
        ${pools.length === 0 && !loading
            ? html`<p style="color: var(--pico-muted-color)">No outbound binds configured. <a href="#/binds">Add a bind</a>.</p>`
            : html`
                <table>
                    <thead>
                        <tr>
                            <th>Name</th>
                            <th>Host</th>
                            <th>System ID</th>
                            <th>Bind Mode</th>
                            <th>Connections</th>
                            <th>Active</th>
                            <th>Health</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${pools.map(p => html`
                            <tr key=${p.name}>
                                <td><code><a href="#/binds">${p.name}</a></code></td>
                                <td>${p.host}:${p.port}</td>
                                <td>${p.system_id}</td>
                                <td>${p.bind_mode || 'transceiver'}</td>
                                <td>${p.connections}</td>
                                <td>${p.active_connections}</td>
                                <td>${p.healthy
                                    ? html`<span class="status-badge badge-green">Healthy</span>`
                                    : html`<span class="status-badge badge-red">Unhealthy</span>`}</td>
                            </tr>
                        `)}
                    </tbody>
                </table>
            `
        }
    `;
}

// ---------------------------------------------------------------------------
// Connection Configs page (Clients)
// ---------------------------------------------------------------------------

function ConnConfigsPage() {
    const [configs, setConfigs] = useState([]);
    const [conns, setConns] = useState([]);
    const [loading, setLoading] = useState(true);
    const [editing, setEditing] = useState(null);

    // Form state
    const [systemId, setSystemId] = useState('');
    const [password, setPassword] = useState('');
    const [description, setDescription] = useState('');
    const [enabled, setEnabled] = useState(true);
    const [allowedIps, setAllowedIps] = useState('');
    const [maxTps, setMaxTps] = useState('100');
    const [costPerSms, setCostPerSms] = useState('0');
    const [allowedPrefixes, setAllowedPrefixes] = useState('');
    const [defaultSourceAddr, setDefaultSourceAddr] = useState('');
    const [sourceAddrMode, setSourceAddrMode] = useState('passthrough');
    const [forceSourceAddr, setForceSourceAddr] = useState('');
    const [allowedSourceAddrs, setAllowedSourceAddrs] = useState('');
    const [maxBinds, setMaxBinds] = useState('2');
    const [bindTransceiver, setBindTransceiver] = useState(true);
    const [bindTransmitter, setBindTransmitter] = useState(true);
    const [bindReceiver, setBindReceiver] = useState(true);

    const resetForm = () => {
        setEditing(null);
        setSystemId('');
        setPassword('');
        setDescription('');
        setEnabled(true);
        setAllowedIps('');
        setMaxTps('100');
        setCostPerSms('0');
        setAllowedPrefixes('');
        setDefaultSourceAddr('');
        setSourceAddrMode('passthrough');
        setForceSourceAddr('');
        setAllowedSourceAddrs('');
        setMaxBinds('2');
        setBindTransceiver(true);
        setBindTransmitter(true);
        setBindReceiver(true);
    };

    const refresh = useCallback(() => {
        setLoading(true);
        Promise.all([
            api('GET', '/admin/api/connconfigs').then(d => setConfigs(d || [])),
            api('GET', '/admin/api/connections').then(d => setConns(d || [])),
        ]).catch(() => {}).finally(() => setLoading(false));
    }, []);

    useEffect(() => { refresh(); }, []);

    const connCountBySystemId = {};
    (conns || []).forEach(c => {
        connCountBySystemId[c.system_id] = (connCountBySystemId[c.system_id] || 0) + 1;
    });

    const startEdit = async (sid) => {
        try {
            const cfg = await api('GET', '/admin/api/connconfigs/' + encodeURIComponent(sid));
            setEditing(sid);
            setSystemId(cfg.system_id);
            setPassword('');
            setDescription(cfg.description || '');
            setEnabled(cfg.enabled);
            setAllowedIps(arrayToText(cfg.allowed_ips));
            setMaxTps(String(cfg.max_tps || 0));
            setCostPerSms(String(cfg.cost_per_sms || 0));
            setAllowedPrefixes(arrayToText(cfg.allowed_prefixes));
            setDefaultSourceAddr(cfg.default_source_addr || '');
            setSourceAddrMode(cfg.source_addr_mode || 'passthrough');
            setForceSourceAddr(cfg.force_source_addr || '');
            setAllowedSourceAddrs(arrayToText(cfg.allowed_source_addrs));
            setMaxBinds(String(cfg.max_binds || 0));
            const modes = cfg.allowed_bind_modes || [];
            setBindTransceiver(modes.length === 0 || modes.includes('transceiver'));
            setBindTransmitter(modes.length === 0 || modes.includes('transmitter'));
            setBindReceiver(modes.length === 0 || modes.includes('receiver'));
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    const submit = async (e) => {
        e.preventDefault();
        const modes = [];
        if (bindTransceiver) modes.push('transceiver');
        if (bindTransmitter) modes.push('transmitter');
        if (bindReceiver) modes.push('receiver');

        const payload = {
            system_id: systemId,
            password: password,
            description,
            enabled,
            allowed_ips: textToArray(allowedIps),
            max_tps: parseInt(maxTps) || 0,
            cost_per_sms: parseFloat(costPerSms) || 0,
            allowed_prefixes: textToArray(allowedPrefixes),
            default_source_addr: defaultSourceAddr,
            source_addr_mode: sourceAddrMode,
            force_source_addr: forceSourceAddr,
            allowed_source_addrs: textToArray(allowedSourceAddrs),
            max_binds: parseInt(maxBinds) || 0,
            allowed_bind_modes: modes,
        };

        try {
            if (editing) {
                await api('PUT', '/admin/api/connconfigs/' + encodeURIComponent(editing), payload);
                showToast('Client config updated');
            } else {
                await api('POST', '/admin/api/connconfigs', payload);
                showToast('Client config created');
            }
            resetForm();
            refresh();
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    const deleteConfig = async (sid) => {
        if (!confirm(`Delete client config "${sid}"?`)) return;
        try {
            await api('DELETE', '/admin/api/connconfigs/' + encodeURIComponent(sid));
            showToast('Client config deleted');
            refresh();
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    return html`
        <h2>Clients</h2>

        <div class="form-card">
            <h4>${editing ? `Edit: ${editing}` : 'Add Client Config'}</h4>
            <form onSubmit=${submit}>
                <div class="form-grid">
                    <label>System ID
                        <input type="text" value=${systemId}
                               onInput=${e => setSystemId(e.target.value)}
                               required disabled=${!!editing} />
                    </label>
                    <label>Password${editing ? ' (blank = keep existing)' : ''}
                        <input type="password" value=${password}
                               onInput=${e => setPassword(e.target.value)}
                               required=${!editing} />
                    </label>
                    <label>Description
                        <input type="text" value=${description}
                               onInput=${e => setDescription(e.target.value)} />
                    </label>
                </div>
                <div class="form-grid">
                    <label>Max TPS
                        <input type="number" value=${maxTps} min="0"
                               placeholder="0 = unlimited"
                               onInput=${e => setMaxTps(e.target.value)} />
                    </label>
                    <label>Cost / SMS
                        <input type="number" value=${costPerSms} min="0" step="0.01"
                               onInput=${e => setCostPerSms(e.target.value)} />
                    </label>
                    <label>Max Binds
                        <input type="number" value=${maxBinds} min="0"
                               placeholder="0 = unlimited"
                               onInput=${e => setMaxBinds(e.target.value)} />
                    </label>
                    <label>Default Source Addr
                        <input type="text" value=${defaultSourceAddr}
                               placeholder="Used when mode=default"
                               onInput=${e => setDefaultSourceAddr(e.target.value)} />
                    </label>
                </div>
                <div class="form-grid">
                    <label>Source Address Mode
                        <select value=${sourceAddrMode} onChange=${e => setSourceAddrMode(e.target.value)}>
                            <option value="passthrough">Passthrough (forward as-is)</option>
                            <option value="default">Default (fill empty source)</option>
                            <option value="override">Override (always replace)</option>
                            <option value="whitelist">Whitelist (restrict allowed)</option>
                        </select>
                    </label>
                    ${sourceAddrMode === 'override' ? html`
                        <label>Force Source Addr
                            <input type="text" value=${forceSourceAddr}
                                   placeholder="Replaces client's source"
                                   onInput=${e => setForceSourceAddr(e.target.value)} />
                        </label>
                    ` : ''}
                    ${sourceAddrMode === 'whitelist' ? html`
                        <label>Allowed Source Addrs
                            <textarea rows="3" value=${allowedSourceAddrs}
                                      placeholder="One address per line"
                                      onInput=${e => setAllowedSourceAddrs(e.target.value)}></textarea>
                        </label>
                    ` : ''}
                </div>
                <div class="form-grid">
                    <label>Allowed IPs
                        <textarea rows="3" value=${allowedIps}
                                  placeholder="One IP per line, empty = allow all"
                                  onInput=${e => setAllowedIps(e.target.value)}></textarea>
                    </label>
                    <label>Allowed Prefixes
                        <textarea rows="3" value=${allowedPrefixes}
                                  placeholder="One prefix per line, empty = allow all"
                                  onInput=${e => setAllowedPrefixes(e.target.value)}></textarea>
                    </label>
                </div>
                <div style="margin-bottom: 0.75rem">
                    <label class="checkbox-label">
                        <input type="checkbox" checked=${enabled}
                               onChange=${e => setEnabled(e.target.checked)} />
                        Enabled
                    </label>
                </div>
                <div>
                    <label style="margin-bottom: 0.3rem">Allowed Bind Modes</label>
                    <div class="checkbox-group">
                        <label class="checkbox-label">
                            <input type="checkbox" checked=${bindTransceiver}
                                   onChange=${e => setBindTransceiver(e.target.checked)} />
                            Transceiver
                        </label>
                        <label class="checkbox-label">
                            <input type="checkbox" checked=${bindTransmitter}
                                   onChange=${e => setBindTransmitter(e.target.checked)} />
                            Transmitter
                        </label>
                        <label class="checkbox-label">
                            <input type="checkbox" checked=${bindReceiver}
                                   onChange=${e => setBindReceiver(e.target.checked)} />
                            Receiver
                        </label>
                    </div>
                </div>
                <button type="submit">${editing ? 'Update' : 'Create'}</button>
                ${editing && html`
                    <button type="button" class="secondary" style="margin-left: 0.5rem"
                            onClick=${resetForm}>Cancel</button>
                `}
            </form>
        </div>

        ${configs.length === 0 && !loading
            ? html`<p>No client configs.</p>`
            : html`
                <table>
                    <thead>
                        <tr>
                            <th>System ID</th>
                            <th>Description</th>
                            <th>Enabled</th>
                            <th>Active</th>
                            <th>Max TPS</th>
                            <th>Cost/SMS</th>
                            <th>Allowed Prefixes</th>
                            <th>Max Binds</th>
                            <th>Created</th>
                            <th></th>
                        </tr>
                    </thead>
                    <tbody>
                        ${configs.map(c => html`
                            <tr key=${c.system_id}>
                                <td><code>${c.system_id}</code></td>
                                <td>${c.description || ''}</td>
                                <td>${statusBadge(c.enabled ? 'healthy' : 'unhealthy')}</td>
                                <td>${connCountBySystemId[c.system_id]
                                    ? html`<span style="color: #22c55e; margin-right: 0.3rem">●</span>${connCountBySystemId[c.system_id]}`
                                    : html`<span style="color: #888">0</span>`
                                }</td>
                                <td>${c.max_tps || 'unlimited'}</td>
                                <td>${c.cost_per_sms ? c.cost_per_sms.toFixed(2) : '0.00'}</td>
                                <td>${(() => {
                                    const p = c.allowed_prefixes || [];
                                    if (p.length === 0) return 'all';
                                    const shown = p.slice(0, 3).join(', ');
                                    return p.length > 3 ? shown + '...' : shown;
                                })()}</td>
                                <td>${c.max_binds || 'unlimited'}</td>
                                <td>${new Date(c.created_at).toLocaleDateString()}</td>
                                <td>
                                    <button class="edit-btn secondary"
                                            onClick=${() => startEdit(c.system_id)}>
                                        Edit
                                    </button>
                                    <button class="danger-btn"
                                            onClick=${() => deleteConfig(c.system_id)}>
                                        Delete
                                    </button>
                                </td>
                            </tr>
                        `)}
                    </tbody>
                </table>
            `
        }
    `;
}

// ---------------------------------------------------------------------------
// Binds page (outbound SMSC connections)
// ---------------------------------------------------------------------------

function BindsPage() {
    const [pools, setPools] = useState([]);
    const [loading, setLoading] = useState(true);

    // Form state
    const [name, setName] = useState('');
    const [bindType, setBindType] = useState('smpp');
    const [host, setHost] = useState('');
    const [port, setPort] = useState('2775');
    const [systemId, setSystemId] = useState('');
    const [password, setPassword] = useState('');
    const [sourceAddr, setSourceAddr] = useState('');
    const [connections, setConnections] = useState('2');
    const [windowSize, setWindowSize] = useState('10');
    const [bindMode, setBindMode] = useState('transceiver');
    const [interfaceVersion, setInterfaceVersion] = useState('3.4');
    const [tlsEnabled, setTlsEnabled] = useState(false);
    const [grpcAddress, setGrpcAddress] = useState('');

    const resetForm = () => {
        setName(''); setBindType('smpp'); setHost(''); setPort('2775'); setSystemId('');
        setPassword(''); setSourceAddr(''); setConnections('2');
        setWindowSize('10'); setBindMode('transceiver');
        setInterfaceVersion('3.4'); setTlsEnabled(false); setGrpcAddress('');
    };

    const refresh = useCallback(() => {
        setLoading(true);
        api('GET', '/admin/api/pools')
            .then(d => setPools(d || []))
            .catch(() => {})
            .finally(() => setLoading(false));
    }, []);

    useEffect(() => { refresh(); }, []);

    const createPool = async (e) => {
        e.preventDefault();
        try {
            const body = { name, bind_type: bindType };
            if (bindType === 'grpc') {
                body.grpc_address = grpcAddress;
            } else {
                body.host = host;
                body.port = parseInt(port) || 2775;
                body.system_id = systemId;
                body.password = password;
                body.source_addr = sourceAddr;
                body.connections = parseInt(connections) || 2;
                body.window_size = parseInt(windowSize) || 10;
                body.bind_mode = bindMode;
                body.interface_version = interfaceVersion;
                body.tls_enabled = tlsEnabled;
            }
            await api('POST', '/admin/api/pools', body);
            showToast('Bind created');
            resetForm();
            refresh();
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    const deletePool = async (poolName) => {
        if (!confirm(`Delete bind "${poolName}"?`)) return;
        try {
            await api('DELETE', '/admin/api/pools/' + encodeURIComponent(poolName));
            showToast('Bind deleted');
            refresh();
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    return html`
        <h2>Binds</h2>
        <p style="color: var(--pico-muted-color); margin-top: -0.5rem">Outbound SMSC connections (Gateway → SMSCs / gRPC Adapters)</p>

        <div class="form-card">
            <h4>Add Bind</h4>
            <form onSubmit=${createPool}>
                <div class="form-grid">
                    <label>Name
                        <input type="text" value=${name}
                               onInput=${e => setName(e.target.value)} required />
                    </label>
                    <label>Bind Type
                        <select value=${bindType} onChange=${e => setBindType(e.target.value)}>
                            <option value="smpp">SMPP</option>
                            <option value="grpc">gRPC</option>
                        </select>
                    </label>
                </div>
                ${bindType === 'grpc' ? html`
                    <div class="form-grid">
                        <label>gRPC Address (host:port)
                            <input type="text" value=${grpcAddress}
                                   onInput=${e => setGrpcAddress(e.target.value)}
                                   placeholder="adapter.example.com:50051" required />
                        </label>
                    </div>
                ` : html`
                    <div class="form-grid">
                        <label>Host
                            <input type="text" value=${host}
                                   onInput=${e => setHost(e.target.value)} required />
                        </label>
                        <label>Port
                            <input type="number" value=${port}
                                   onInput=${e => setPort(e.target.value)} required />
                        </label>
                    </div>
                    <div class="form-grid">
                        <label>System ID
                            <input type="text" value=${systemId}
                                   onInput=${e => setSystemId(e.target.value)} required />
                        </label>
                        <label>Password
                            <input type="password" value=${password}
                                   onInput=${e => setPassword(e.target.value)} required />
                        </label>
                        <label>Source Addr
                            <input type="text" value=${sourceAddr}
                                   onInput=${e => setSourceAddr(e.target.value)} />
                        </label>
                    </div>
                    <div class="form-grid">
                        <label>Connections
                            <input type="number" value=${connections} min="1"
                                   onInput=${e => setConnections(e.target.value)} />
                        </label>
                        <label>Window Size
                            <input type="number" value=${windowSize} min="1"
                                   onInput=${e => setWindowSize(e.target.value)} />
                        </label>
                        <label>Bind Mode
                            <select value=${bindMode} onChange=${e => setBindMode(e.target.value)}>
                                <option value="transceiver">Transceiver</option>
                                <option value="transmitter">Transmitter</option>
                                <option value="receiver">Receiver</option>
                            </select>
                        </label>
                        <label>Interface Version
                            <select value=${interfaceVersion} onChange=${e => setInterfaceVersion(e.target.value)}>
                                <option value="3.4">3.4</option>
                                <option value="5.0">5.0</option>
                            </select>
                        </label>
                    </div>
                    <div style="margin-bottom: 0.75rem">
                        <label class="checkbox-label">
                            <input type="checkbox" checked=${tlsEnabled}
                                   onChange=${e => setTlsEnabled(e.target.checked)} />
                            TLS Enabled
                        </label>
                    </div>
                `}
                <button type="submit">Create Bind</button>
            </form>
        </div>

        ${pools.length === 0 && !loading
            ? html`<p>No binds configured.</p>`
            : html`
                <table>
                    <thead>
                        <tr>
                            <th>Name</th>
                            <th>Type</th>
                            <th>Address</th>
                            <th>System ID</th>
                            <th>Conns</th>
                            <th>Window</th>
                            <th>Bind Mode</th>
                            <th>Version</th>
                            <th>Active</th>
                            <th>Health</th>
                            <th></th>
                        </tr>
                    </thead>
                    <tbody>
                        ${pools.map(p => html`
                            <tr key=${p.name}>
                                <td>${p.name}</td>
                                <td>${(p.bind_type || 'smpp').toUpperCase()}</td>
                                <td>${p.bind_type === 'grpc' ? p.grpc_address : (p.host + ':' + p.port)}</td>
                                <td>${p.bind_type === 'grpc' ? '-' : p.system_id}</td>
                                <td>${p.bind_type === 'grpc' ? '-' : p.connections}</td>
                                <td>${p.bind_type === 'grpc' ? '-' : p.window_size}</td>
                                <td>${p.bind_type === 'grpc' ? '-' : (p.bind_mode || 'transceiver')}</td>
                                <td>${p.bind_type === 'grpc' ? '-' : (p.interface_version || '3.4')}</td>
                                <td>${p.active_connections}</td>
                                <td>${statusBadge(p.healthy ? 'healthy' : 'unhealthy')}</td>
                                <td>
                                    <button class="danger-btn"
                                            onClick=${() => deletePool(p.name)}>
                                        Delete
                                    </button>
                                </td>
                            </tr>
                        `)}
                    </tbody>
                </table>
            `
        }
    `;
}

// ---------------------------------------------------------------------------
// Routes page -- MT and MO route CRUD
// ---------------------------------------------------------------------------

function RoutesPage() {
    const [mtRoutes, setMTRoutes] = useState([]);
    const [moRoutes, setMORoutes] = useState([]);
    const [loading, setLoading] = useState(true);
    const [poolNames, setPoolNames] = useState([]);
    const [connections, setConnections] = useState([]);

    // MT form state
    const [mtPrefix, setMTPrefix] = useState('');
    const [mtStrategy, setMTStrategy] = useState('failover');
    const [mtPools, setMTPools] = useState('');
    const [mtSelectedPools, setMTSelectedPools] = useState([]);

    // MO form state
    const [moDestPattern, setMODestPattern] = useState('');
    const [moSourcePrefix, setMOSourcePrefix] = useState('');
    const [moTargetType, setMOTargetType] = useState('http');
    const [moTargetConnID, setMOTargetConnID] = useState('');
    const [moTargetURL, setMOTargetURL] = useState('');
    const [moPriority, setMOPriority] = useState('0');

    const refresh = useCallback(() => {
        setLoading(true);
        Promise.all([
            api('GET', '/admin/api/routes/mt').then(d => setMTRoutes(d || [])),
            api('GET', '/admin/api/routes/mo').then(d => setMORoutes(d || [])),
            api('GET', '/admin/api/pools').then(d => setPoolNames((d || []).map(p => p.name))),
            api('GET', '/admin/api/connections').then(d => setConnections(d || [])),
        ]).catch(() => {}).finally(() => setLoading(false));
    }, []);

    useEffect(() => { refresh(); }, []);

    const togglePool = (name) => {
        setMTSelectedPools(prev =>
            prev.includes(name)
                ? prev.filter(n => n !== name)
                : [...prev, name]
        );
    };

    const addMTRoute = async (e) => {
        e.preventDefault();
        // Build pool list from checkboxes if pools exist, otherwise parse text input
        let pools;
        if (poolNames.length > 0) {
            pools = mtSelectedPools.map(name => ({ name, cost: 0 }));
        } else {
            pools = mtPools.split(',').map(s => s.trim()).filter(Boolean).map(s => {
                const parts = s.split(':');
                return { name: parts[0].trim(), cost: parts[1] ? parseFloat(parts[1]) : 0 };
            });
        }
        if (pools.length === 0) {
            showToast('Select at least one pool', 'error');
            return;
        }
        try {
            await api('POST', '/admin/api/routes/mt', {
                prefix: mtPrefix,
                strategy: mtStrategy,
                pools,
            });
            showToast('MT route created');
            setMTPrefix(''); setMTPools(''); setMTSelectedPools([]);
            refresh();
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    const deleteMTRoute = async (prefix) => {
        if (!confirm(`Delete MT route "${prefix}"?`)) return;
        try {
            await api('DELETE', '/admin/api/routes/mt/' + encodeURIComponent(prefix));
            showToast('MT route deleted');
            refresh();
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    const addMORoute = async (e) => {
        e.preventDefault();
        const target = {
            type: moTargetType,
            conn_id: moTargetType === 'smpp' ? moTargetConnID : '',
            callback_url: moTargetType === 'http' ? moTargetURL : '',
        };
        try {
            await api('POST', '/admin/api/routes/mo', {
                dest_pattern: moDestPattern,
                source_prefix: moSourcePrefix,
                target,
                priority: parseInt(moPriority) || 0,
            });
            showToast('MO route created');
            setMODestPattern(''); setMOSourcePrefix('');
            setMOTargetConnID(''); setMOTargetURL('');
            refresh();
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    const deleteMORoute = async (destPattern, sourcePrefix) => {
        if (!confirm(`Delete MO route "${destPattern}"?`)) return;
        const id = sourcePrefix ? `${destPattern}:${sourcePrefix}` : destPattern;
        try {
            await api('DELETE', '/admin/api/routes/mo/' + encodeURIComponent(id));
            showToast('MO route deleted');
            refresh();
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    return html`
        <h2>Routes</h2>

        <!-- MT Routes -->
        <h3>MT Routes (Outbound)</h3>
        <div class="form-card">
            <h4>Add MT Route</h4>
            <form onSubmit=${addMTRoute}>
                <div class="form-grid">
                    <label>Prefix
                        <input type="text" value=${mtPrefix} placeholder="e.g. 234 or *"
                               onInput=${e => setMTPrefix(e.target.value)} required />
                    </label>
                    <label>Strategy
                        <select value=${mtStrategy} onChange=${e => setMTStrategy(e.target.value)}>
                            <option value="failover">Failover</option>
                            <option value="round_robin">Round Robin</option>
                            <option value="least_cost">Least Cost</option>
                        </select>
                    </label>
                </div>
                ${poolNames.length > 0
                    ? html`
                        <label>Binds</label>
                        <div class="checkbox-group">
                            ${poolNames.map(name => html`
                                <label class="checkbox-label" key=${name}>
                                    <input type="checkbox"
                                           checked=${mtSelectedPools.includes(name)}
                                           onChange=${() => togglePool(name)} />
                                    ${name}
                                </label>
                            `)}
                        </div>
                    `
                    : html`
                        <p style="color: #888">No binds available. <a href="#/binds">Create a bind first</a>.</p>
                    `
                }
                <button type="submit">Add MT Route</button>
            </form>
        </div>

        ${mtRoutes.length === 0 && !loading
            ? html`<p>No MT routes configured.</p>`
            : html`
                <table>
                    <thead>
                        <tr>
                            <th>Prefix</th>
                            <th>Strategy</th>
                            <th>Binds</th>
                            <th></th>
                        </tr>
                    </thead>
                    <tbody>
                        ${mtRoutes.map(r => html`
                            <tr key=${r.prefix}>
                                <td><code>${r.prefix}</code></td>
                                <td>${r.strategy}</td>
                                <td>${(r.pools || []).map(p =>
                                    p.cost ? `${p.name} ($${p.cost})` : p.name
                                ).join(', ')}</td>
                                <td>
                                    <button class="danger-btn"
                                            onClick=${() => deleteMTRoute(r.prefix)}>
                                        Delete
                                    </button>
                                </td>
                            </tr>
                        `)}
                    </tbody>
                </table>
            `
        }

        <!-- MO Routes -->
        <h3 style="margin-top: 2.5rem">MO Routes (Inbound)</h3>
        <div class="form-card">
            <h4>Add MO Route</h4>
            <form onSubmit=${addMORoute}>
                <div class="form-grid">
                    <label>Dest Pattern
                        <input type="text" value=${moDestPattern} placeholder="e.g. 12345 or 123*"
                               onInput=${e => setMODestPattern(e.target.value)} required />
                    </label>
                    <label>Source Prefix (optional)
                        <input type="text" value=${moSourcePrefix} placeholder="e.g. 234"
                               onInput=${e => setMOSourcePrefix(e.target.value)} />
                    </label>
                </div>
                <div class="form-grid">
                    <label>Target Type
                        <select value=${moTargetType} onChange=${e => setMOTargetType(e.target.value)}>
                            <option value="http">HTTP Webhook</option>
                            <option value="smpp">SMPP Connection</option>
                        </select>
                    </label>
                    ${moTargetType === 'smpp'
                        ? html`<label>Connection ID
                            ${connections.length > 0
                                ? html`<select value=${moTargetConnID}
                                               onChange=${e => setMOTargetConnID(e.target.value)}>
                                    <option value="">-- select connection --</option>
                                    ${[...new Set(connections.map(c => c.system_id))].map(sid => html`
                                        <option key=${sid} value=${sid}>${sid}</option>
                                    `)}
                                  </select>`
                                : html`<input type="text" value=${moTargetConnID}
                                              placeholder="connection ID"
                                              onInput=${e => setMOTargetConnID(e.target.value)} required />`
                            }
                          </label>`
                        : html`<label>Callback URL
                            <input type="url" value=${moTargetURL} placeholder="https://..."
                                   onInput=${e => setMOTargetURL(e.target.value)} required />
                          </label>`
                    }
                </div>
                <label>Priority
                    <input type="number" value=${moPriority}
                           onInput=${e => setMOPriority(e.target.value)}
                           style="width: 120px" />
                </label>
                <button type="submit">Add MO Route</button>
            </form>
        </div>

        ${moRoutes.length === 0 && !loading
            ? html`<p>No MO routes configured.</p>`
            : html`
                <table>
                    <thead>
                        <tr>
                            <th>Dest Pattern</th>
                            <th>Source Prefix</th>
                            <th>Target</th>
                            <th>Priority</th>
                            <th></th>
                        </tr>
                    </thead>
                    <tbody>
                        ${moRoutes.map(r => html`
                            <tr key=${r.dest_pattern + ':' + r.source_prefix}>
                                <td><code>${r.dest_pattern}</code></td>
                                <td>${r.source_prefix || '(any)'}</td>
                                <td>${r.target.type === 'http'
                                    ? html`HTTP: <code>${r.target.callback_url}</code>`
                                    : html`SMPP: <code>${r.target.conn_id}</code>`
                                }</td>
                                <td>${r.priority}</td>
                                <td>
                                    <button class="danger-btn"
                                            onClick=${() => deleteMORoute(r.dest_pattern, r.source_prefix)}>
                                        Delete
                                    </button>
                                </td>
                            </tr>
                        `)}
                    </tbody>
                </table>
            `
        }
    `;
}

// ---------------------------------------------------------------------------
// Messages page
// ---------------------------------------------------------------------------

function MessagesPage() {
    const [messages, setMessages] = useState([]);
    const [loading, setLoading] = useState(false);
    const [conns, setConns] = useState([]);
    const [resultCount, setResultCount] = useState(0);

    // Search form state
    const [connId, setConnId] = useState('');
    const [status, setStatus] = useState('');
    const [source, setSource] = useState('');
    const [dest, setDest] = useState('');
    const [after, setAfter] = useState('');
    const [before, setBefore] = useState('');

    useEffect(() => {
        api('GET', '/admin/api/connections').then(d => setConns(d || [])).catch(() => {});
        doSearch();
    }, []);

    const uniqueSystemIds = [...new Set((conns || []).map(c => c.system_id))];

    const buildQuery = (extraParams = {}) => {
        const params = new URLSearchParams();
        if (connId) params.set('conn_id', connId);
        if (status) params.set('status', status);
        if (source) params.set('from', source);
        if (dest) params.set('to', dest);
        if (after) params.set('after', new Date(after).toISOString());
        if (before) params.set('before', new Date(before).toISOString());
        params.set('limit', '50');
        for (const [k, v] of Object.entries(extraParams)) {
            params.set(k, v);
        }
        return params.toString();
    };

    const doSearch = async (e) => {
        if (e) e.preventDefault();
        setLoading(true);
        try {
            const data = await api('GET', '/admin/api/messages?' + buildQuery());
            setMessages(data || []);
            setResultCount((data || []).length);
        } catch (err) {
            showToast(err.message, 'error');
        } finally {
            setLoading(false);
        }
    };

    const loadMore = async () => {
        if (messages.length === 0) return;
        const lastMsg = messages[messages.length - 1];
        setLoading(true);
        try {
            const data = await api('GET', '/admin/api/messages?' + buildQuery({
                before: lastMsg.created_at,
            }));
            if (data && data.length > 0) {
                setMessages(prev => [...prev, ...data]);
                setResultCount(prev => prev + data.length);
            }
        } catch (err) {
            showToast(err.message, 'error');
        } finally {
            setLoading(false);
        }
    };

    return html`
        <h2>Messages</h2>

        <div class="form-card">
            <form onSubmit=${doSearch}>
                <div class="search-form">
                    <label>Connection
                        <select value=${connId} onChange=${e => setConnId(e.target.value)}>
                            <option value="">All</option>
                            ${uniqueSystemIds.map(sid => html`
                                <option key=${sid} value=${sid}>${sid}</option>
                            `)}
                        </select>
                    </label>
                    <label>Status
                        <select value=${status} onChange=${e => setStatus(e.target.value)}>
                            <option value="">All</option>
                            <option value="accepted">Accepted</option>
                            <option value="forwarded">Forwarded</option>
                            <option value="delivered">Delivered</option>
                            <option value="failed">Failed</option>
                            <option value="rejected">Rejected</option>
                        </select>
                    </label>
                    <label>Source
                        <input type="text" value=${source} placeholder="Source address"
                               onInput=${e => setSource(e.target.value)} />
                    </label>
                    <label>Destination
                        <input type="text" value=${dest} placeholder="Destination address"
                               onInput=${e => setDest(e.target.value)} />
                    </label>
                    <label>After
                        <input type="datetime-local" value=${after}
                               onInput=${e => setAfter(e.target.value)} />
                    </label>
                    <label>Before
                        <input type="datetime-local" value=${before}
                               onInput=${e => setBefore(e.target.value)} />
                    </label>
                </div>
                <button type="submit" aria-busy=${loading}>Search</button>
            </form>
        </div>

        ${resultCount > 0 && html`<p><strong>${resultCount}</strong> messages found</p>`}

        ${messages.length === 0 && !loading
            ? html`<p>No messages found.</p>`
            : html`
                <table>
                    <thead>
                        <tr>
                            <th>Time</th>
                            <th>Message ID</th>
                            <th>Connection</th>
                            <th>Source / Dest</th>
                            <th>Status</th>
                            <th>DLR Status</th>
                            <th>Cost</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${messages.map(m => html`
                            <tr key=${m.gw_msg_id}>
                                <td>${formatTime(m.created_at)}</td>
                                <td title=${m.gw_msg_id}><code>${(m.gw_msg_id || '').substring(0, 12)}</code></td>
                                <td>${m.conn_id}</td>
                                <td>${m.source_addr}<span class="message-arrow"> → </span>${m.dest_addr}</td>
                                <td>${statusBadge(m.status)}</td>
                                <td>${m.dlr_status || ''}</td>
                                <td>${m.cost > 0 ? m.cost.toFixed(2) : ''}</td>
                            </tr>
                        `)}
                    </tbody>
                </table>

                ${messages.length >= 50 && html`
                    <button onClick=${loadMore} aria-busy=${loading}
                            style="margin-top: 0.5rem">
                        Load more
                    </button>
                `}
            `
        }
    `;
}

// ---------------------------------------------------------------------------
// API Keys page -- create / revoke
// ---------------------------------------------------------------------------

function APIKeysPage() {
    const [keys, setKeys] = useState([]);
    const [label, setLabel] = useState('');
    const [rateLimit, setRateLimit] = useState('0');
    const [newKey, setNewKey] = useState('');
    const [loading, setLoading] = useState(true);

    const refresh = useCallback(() => {
        setLoading(true);
        api('GET', '/admin/api/apikeys')
            .then(data => setKeys(data || []))
            .catch(() => {})
            .finally(() => setLoading(false));
    }, []);

    useEffect(() => { refresh(); }, []);

    const createKey = async (e) => {
        e.preventDefault();
        try {
            const data = await api('POST', '/admin/api/apikeys', {
                label,
                rate_limit: parseInt(rateLimit) || 0,
            });
            setNewKey(data.key);
            setLabel('');
            setRateLimit('0');
            showToast('API key created');
            refresh();
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    const revokeKey = async (id) => {
        if (!confirm(`Revoke API key "${id}"?`)) return;
        try {
            await api('DELETE', '/admin/api/apikeys/' + encodeURIComponent(id));
            showToast('API key revoked');
            refresh();
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    return html`
        <h2>API Keys</h2>

        <details open>
            <summary>Create API Key</summary>
            <form class="inline-form" onSubmit=${createKey}>
                <label>Label
                    <input type="text" value=${label} placeholder="e.g. Production"
                           onInput=${e => setLabel(e.target.value)} required
                           style="width: 200px" />
                </label>
                <label>Rate Limit (TPS, 0=unlimited)
                    <input type="number" value=${rateLimit} min="0"
                           onInput=${e => setRateLimit(e.target.value)}
                           style="width: 120px" />
                </label>
                <button type="submit">Create</button>
            </form>
        </details>

        ${newKey && html`
            <div class="new-key-display">
                <strong>New API Key (copy now, shown only once):</strong><br/>
                <code>${newKey}</code>
                <br/><br/>
                <button style="font-size: 0.85rem; padding: 0.3rem 0.8rem"
                        onClick=${() => { navigator.clipboard.writeText(newKey); showToast('Copied to clipboard'); }}>
                    Copy
                </button>
                <button style="font-size: 0.85rem; padding: 0.3rem 0.8rem; margin-left: 0.5rem"
                        class="secondary"
                        onClick=${() => setNewKey('')}>
                    Dismiss
                </button>
            </div>
        `}

        ${keys.length === 0 && !loading
            ? html`<p>No API keys.</p>`
            : html`
                <table>
                    <thead>
                        <tr>
                            <th>ID</th>
                            <th>Label</th>
                            <th>Rate Limit</th>
                            <th>Created</th>
                            <th>Last Used</th>
                            <th></th>
                        </tr>
                    </thead>
                    <tbody>
                        ${keys.map(k => html`
                            <tr key=${k.id}>
                                <td><code>${k.id}</code></td>
                                <td>${k.label}</td>
                                <td>${k.rate_limit ? k.rate_limit + ' TPS' : 'Unlimited'}</td>
                                <td>${new Date(k.created_at).toLocaleDateString()}</td>
                                <td>${k.last_used && k.last_used !== '0001-01-01T00:00:00Z'
                                    ? new Date(k.last_used).toLocaleString()
                                    : 'Never'}</td>
                                <td>
                                    <button class="danger-btn"
                                            onClick=${() => revokeKey(k.id)}>
                                        Revoke
                                    </button>
                                </td>
                            </tr>
                        `)}
                    </tbody>
                </table>
            `
        }
    `;
}

// ---------------------------------------------------------------------------
// Users page -- create / delete / change password
// ---------------------------------------------------------------------------

function UsersPage() {
    const [users, setUsers] = useState([]);
    const [loading, setLoading] = useState(true);

    // Create form
    const [newUsername, setNewUsername] = useState('');
    const [newPassword, setNewPassword] = useState('');
    const [newRole, setNewRole] = useState('admin');

    // Password change form
    const [changePwUser, setChangePwUser] = useState('');
    const [oldPw, setOldPw] = useState('');
    const [newPw, setNewPw] = useState('');

    const refresh = useCallback(() => {
        setLoading(true);
        api('GET', '/admin/api/users')
            .then(data => setUsers(data || []))
            .catch(() => {})
            .finally(() => setLoading(false));
    }, []);

    useEffect(() => { refresh(); }, []);

    const createUser = async (e) => {
        e.preventDefault();
        try {
            await api('POST', '/admin/api/users', {
                username: newUsername,
                password: newPassword,
                role: newRole,
            });
            showToast('User created');
            setNewUsername(''); setNewPassword('');
            refresh();
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    const deleteUser = async (username) => {
        if (!confirm(`Delete user "${username}"?`)) return;
        try {
            await api('DELETE', '/admin/api/users/' + encodeURIComponent(username));
            showToast('User deleted');
            refresh();
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    const changePassword = async (e) => {
        e.preventDefault();
        try {
            await api('PUT', '/admin/api/users/' + encodeURIComponent(changePwUser) + '/password', {
                old_password: oldPw,
                new_password: newPw,
            });
            showToast('Password changed');
            setChangePwUser(''); setOldPw(''); setNewPw('');
        } catch (err) {
            showToast(err.message, 'error');
        }
    };

    return html`
        <h2>Users</h2>

        <details open>
            <summary>Create User</summary>
            <form class="inline-form" onSubmit=${createUser}>
                <label>Username
                    <input type="text" value=${newUsername}
                           onInput=${e => setNewUsername(e.target.value)} required
                           style="width: 160px" />
                </label>
                <label>Password
                    <input type="password" value=${newPassword}
                           onInput=${e => setNewPassword(e.target.value)} required
                           style="width: 160px" />
                </label>
                <label>Role
                    <select value=${newRole} onChange=${e => setNewRole(e.target.value)}>
                        <option value="admin">Admin</option>
                    </select>
                </label>
                <button type="submit">Create</button>
            </form>
        </details>

        ${users.length === 0 && !loading
            ? html`<p>No users.</p>`
            : html`
                <table>
                    <thead>
                        <tr>
                            <th>Username</th>
                            <th>Role</th>
                            <th>Created</th>
                            <th></th>
                        </tr>
                    </thead>
                    <tbody>
                        ${users.map(u => html`
                            <tr key=${u.username}>
                                <td><code>${u.username}</code></td>
                                <td>${u.role}</td>
                                <td>${new Date(u.created_at).toLocaleDateString()}</td>
                                <td>
                                    <button style="font-size: 0.85rem; padding: 0.3rem 0.6rem; margin-right: 0.3rem"
                                            class="secondary"
                                            onClick=${() => { setChangePwUser(u.username); setOldPw(''); setNewPw(''); }}>
                                        Change Password
                                    </button>
                                    <button class="danger-btn"
                                            onClick=${() => deleteUser(u.username)}>
                                        Delete
                                    </button>
                                </td>
                            </tr>
                        `)}
                    </tbody>
                </table>
            `
        }

        ${changePwUser && html`
            <article style="margin-top: 1rem">
                <h4>Change Password: ${changePwUser}</h4>
                <form class="inline-form" onSubmit=${changePassword}>
                    <label>Current Password
                        <input type="password" value=${oldPw}
                               onInput=${e => setOldPw(e.target.value)} required
                               style="width: 180px" />
                    </label>
                    <label>New Password
                        <input type="password" value=${newPw}
                               onInput=${e => setNewPw(e.target.value)} required
                               style="width: 180px" />
                    </label>
                    <button type="submit">Change</button>
                    <button type="button" class="secondary"
                            onClick=${() => setChangePwUser('')}>Cancel</button>
                </form>
            </article>
        `}
    `;
}

// ---------------------------------------------------------------------------
// App shell with hash routing
// ---------------------------------------------------------------------------

function App() {
    const [route, setRoute] = useState(location.hash || '#/dashboard');
    const [appVersion, setAppVersion] = useState('');

    useEffect(() => {
        const handler = () => setRoute(location.hash || '#/dashboard');
        window.addEventListener('hashchange', handler);
        // Fetch version once
        api('GET', '/admin/api/stats').then(s => { if (s && s.version) setAppVersion(s.version); }).catch(() => {});
        return () => window.removeEventListener('hashchange', handler);
    }, []);

    const jwt = localStorage.getItem('jwt');
    if (!jwt && route !== '#/login') {
        location.hash = '#/login';
        return html`<${LoginPage} />`;
    }

    if (route === '#/login') return html`
        <${LoginPage} />
        <${ToastContainer} />
    `;

    const logout = (e) => {
        e.preventDefault();
        localStorage.removeItem('jwt');
        location.hash = '#/login';
    };

    const pages = {
        '#/dashboard': DashboardPage,
        '#/connections': ConnectionsPage,
        '#/connconfigs': ConnConfigsPage,
        '#/binds': BindsPage,
        '#/routes': RoutesPage,
        '#/messages': MessagesPage,
        '#/apikeys': APIKeysPage,
        '#/users': UsersPage,
    };

    const PageComponent = pages[route] || DashboardPage;

    return html`
        <nav class="app-nav">
            <span class="brand">SMSC Gateway${appVersion ? html` <span class="version-tag">v${appVersion}</span>` : ''}</span>
            <a href="#/dashboard" class=${route === '#/dashboard' ? 'active' : ''}>Dashboard</a>
            <a href="#/connections" class=${route === '#/connections' ? 'active' : ''}>Connections</a>
            <a href="#/connconfigs" class=${route === '#/connconfigs' ? 'active' : ''}>Clients</a>
            <a href="#/binds" class=${route === '#/binds' ? 'active' : ''}>Binds</a>
            <a href="#/routes" class=${route === '#/routes' ? 'active' : ''}>Routes</a>
            <a href="#/messages" class=${route === '#/messages' ? 'active' : ''}>Messages</a>
            <a href="#/apikeys" class=${route === '#/apikeys' ? 'active' : ''}>API Keys</a>
            <a href="#/users" class=${route === '#/users' ? 'active' : ''}>Users</a>
            <a href="#" onClick=${logout}>Logout</a>
        </nav>
        <main class="container">
            <${PageComponent} />
        </main>
        <${ToastContainer} />
    `;
}

render(html`<${App} />`, document.getElementById('app'));

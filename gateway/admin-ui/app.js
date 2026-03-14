import { h, render } from 'https://esm.sh/preact@10.19.3';
import { useState, useEffect, useCallback } from 'https://esm.sh/preact@10.19.3/hooks';
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
// Dashboard page — real-time WebSocket updates
// ---------------------------------------------------------------------------

function fmt(n) {
    return (n || 0).toLocaleString();
}

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

    useEffect(() => {
        // Fetch initial stats via REST
        api('GET', '/admin/api/stats').then(setStats).catch(() => {});

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
            <${StatCard} value=${pools.length} label="Pools"
                sublabel="configured" title="Number of southbound SMSC pools" />
        </div>

        <h3>Pool Health</h3>
        ${pools.length === 0
            ? html`<p>No pools configured.</p>`
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
                                <td>${p.name}</td>
                                <td>${p.active_connections}</td>
                                <td class=${p.healthy ? 'status-healthy' : 'status-unhealthy'}>
                                    ${p.healthy ? 'Healthy' : 'Unhealthy'}
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
// Connections page
// ---------------------------------------------------------------------------

function ConnectionsPage() {
    const [conns, setConns] = useState([]);
    const [loading, setLoading] = useState(true);

    const refresh = useCallback(() => {
        setLoading(true);
        api('GET', '/admin/api/connections')
            .then(data => setConns(data || []))
            .catch(() => {})
            .finally(() => setLoading(false));
    }, []);

    useEffect(() => { refresh(); }, []);

    return html`
        <h2>Connections
            <button style="margin-left: 1rem; font-size: 0.8rem; padding: 0.3rem 0.8rem"
                    onClick=${refresh} aria-busy=${loading}>
                Refresh
            </button>
        </h2>
        ${conns.length === 0 && !loading
            ? html`<p>No active connections.</p>`
            : html`
                <table>
                    <thead>
                        <tr>
                            <th>ID</th>
                            <th>System ID</th>
                            <th>Remote Address</th>
                            <th>Bound Since</th>
                            <th>In-Flight</th>
                        </tr>
                    </thead>
                    <tbody>
                        ${conns.map(c => html`
                            <tr key=${c.id}>
                                <td><code>${c.id}</code></td>
                                <td>${c.system_id}</td>
                                <td>${c.remote_addr}</td>
                                <td>${new Date(c.bound_since).toLocaleString()}</td>
                                <td>${c.in_flight}</td>
                            </tr>
                        `)}
                    </tbody>
                </table>
            `
        }
    `;
}

// ---------------------------------------------------------------------------
// Routes page — MT and MO route CRUD
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
            api('GET', '/admin/api/stats').then(d => setPoolNames(d.pool_names || [])),
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
                        <label>Pools</label>
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
                        <label>Pools (comma-separated, name or name:cost)
                            <input type="text" value=${mtPools}
                                   placeholder="e.g. pool-a, pool-b:0.05"
                                   onInput=${e => setMTPools(e.target.value)} required />
                        </label>
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
                            <th>Pools</th>
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
                                    ${connections.map(c => html`
                                        <option key=${c.id} value=${c.id}>${c.id} (${c.system_id})</option>
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
// API Keys page — create / revoke
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
// Users page — create / delete / change password
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

    useEffect(() => {
        const handler = () => setRoute(location.hash || '#/dashboard');
        window.addEventListener('hashchange', handler);
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
        '#/routes': RoutesPage,
        '#/apikeys': APIKeysPage,
        '#/users': UsersPage,
    };

    const PageComponent = pages[route] || DashboardPage;

    return html`
        <nav class="app-nav">
            <span class="brand">SMSC Gateway</span>
            <a href="#/dashboard" class=${route === '#/dashboard' ? 'active' : ''}>Dashboard</a>
            <a href="#/connections" class=${route === '#/connections' ? 'active' : ''}>Connections</a>
            <a href="#/routes" class=${route === '#/routes' ? 'active' : ''}>Routes</a>
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

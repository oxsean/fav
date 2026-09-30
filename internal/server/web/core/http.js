// http is the page's plain HTTP: the browser session (/session, /login, /logout), the sign-in methods and invitation
// (/auth/*), this browser's sign-in from another device and one to allow (/auth/device, /api/device), the team's
// people, admission, invitations and audit log, the machines' node tokens, the projects' tracker bindings, the
// viewer's own tokens, sessions, linked accounts and webhook (/api/*), and the skins' presets (/theme/presets.json).
// Writes carry X-Tend, which the server asks of a browser.

export class HTTPError extends Error {
  constructor(status, code) {
    super(`${status} ${code}`);
    this.status = status;
    this.code = code;
  }
}

// createHTTP: fetch is the page's (the tests pass their own).
export function createHTTP({fetch = (...a) => globalThis.fetch(...a)} = {}) {
  async function ask(method, path, body, form = false) {
    const headers = {};
    if (method !== 'GET') headers['X-Tend'] = '1';
    if (body !== undefined) headers['Content-Type'] = form ? 'application/x-www-form-urlencoded' : 'application/json';
    let res;
    try {
      res = await fetch(path, {method, headers, credentials: 'same-origin',
        body: body === undefined ? undefined : form ? new URLSearchParams(body).toString() : JSON.stringify(body)});
    } catch {
      throw new HTTPError(0, 'offline');
    }
    const text = await res.text();
    let data = null;
    try { data = text ? JSON.parse(text) : null; } catch {}
    if (!res.ok) throw new HTTPError(res.status, data?.error || (res.status === 401 ? 'unauthorized' : 'internal'));
    return data;
  }

  return {
    // session is who the browser is signed in as ({id, name, email, username, role, session}), or null.
    async session() {
      try { return await ask('GET', '/session'); } catch (e) {
        if (e.status === 401) return null;
        throw e;
      }
    },
    login: token => ask('POST', '/login', {token}, true),
    // pushKey is the server's Web Push public key, base64url (503 push_key when it has none); putDevice registers or
    // renews this browser's push subscription (PushSubscription.toJSON()), dropDevice forgets it by its endpoint.
    pushKey: async () => (await ask('GET', '/api/push/key')).key,
    putDevice: (subscription, name) => ask('PUT', '/api/push/device', {subscription, name}),
    dropDevice: endpoint => ask('DELETE', '/api/push/device', {endpoint}),
    // devices are the viewer's push devices ({id, name, created, renewed, last_ok, failures, prefs: {events, hide, wait}});
    // setPrefs changes what one of them wants, removeDevice takes one away.
    devices: () => ask('GET', '/api/push/devices'),
    setPrefs: (id, prefs) => ask('POST', '/api/push/prefs', {id, prefs}),
    removeDevice: id => ask('DELETE', '/api/push/devices', {id}),
    logout: () => ask('POST', '/logout', {}, true),
    // logins are the sign-in methods this server offers: [{name, display}].
    logins: async () => (await ask('GET', '/auth/logins')) || [],
    // invite is what an invitation link holds: {inviter, role, project?, access?, expires}.
    invite: code => ask('GET', '/auth/invite?code=' + encodeURIComponent(code)),
    // askDevice starts this browser's sign-in from another device → {device_code, user_code, verify_url, interval, expires_in};
    // pollDevice asks how it stands: {status: pending | denied | expired} or {status: ok, user}, the session cookie set.
    askDevice: name => ask('POST', '/auth/device', {name, session: true}),
    pollDevice: code => ask('POST', '/auth/device/token', {device_code: code}),
    // device is a pending sign-in to allow: {code, name, ip, created, session?} (session: a browser, not a terminal).
    device: code => ask('GET', '/api/device?code=' + encodeURIComponent(code)),
    decideDevice: (code, allow) => ask('POST', '/api/device', {code, allow}),
    // users are the people on this server: [{id, name, username, role, disabled}].
    users: async () => (await ask('GET', '/api/users')) || [],
    // setUser changes a user's role (member | admin) or disables them ({id, role?, disabled?}); admins only.
    setUser: p => ask('POST', '/api/users', p),
    // offboard hands a user's work to another ({user, to}), disables them and ends their credentials; admins only.
    offboard: p => ask('POST', '/api/users/offboard', p),
    // admits are who may sign in without an invitation: [{kind: email | domain | login, value, role}]; admins only.
    admits: async () => (await ask('GET', '/api/admits')) || [],
    addAdmit: a => ask('POST', '/api/admits', a),
    removeAdmit: a => ask('DELETE', '/api/admits', a),
    // invites are the one-time links not used yet; makeInvite makes one ({role, project?, access?}) → {url, expires}.
    invites: async () => (await ask('GET', '/api/invites')) || [],
    makeInvite: p => ask('POST', '/api/invites', p),
    revokeInvite: id => ask('DELETE', '/api/invites', {id}),
    // audit is the security log, newest first (the last 200); admins only.
    audit: async () => (await ask('GET', '/api/audit')) || [],
    // machineCreds are the node tokens of the caller's machines (an admin's: everyone's).
    machineCreds: async () => (await ask('GET', '/api/machines')) || [],
    // addMachine makes a node token for a new machine of the caller's → {id, token, command}.
    addMachine: name => ask('POST', '/api/machines', {name}),
    revokeMachine: id => ask('DELETE', '/api/machines', {id}),
    rebindMachine: id => ask('POST', '/api/machines/rebind', {id}),
    // tokens are the caller's personal tokens and browser sessions: [{id, kind: token | web, name, created, last_used,
    // last_ip, agent (a session's User-Agent), expires, current}]; addToken makes a token → {id, token}, shown once; revokeToken ends one of either kind.
    tokens: async () => (await ask('GET', '/api/tokens')) || [],
    addToken: name => ask('POST', '/api/tokens', {name}),
    revokeToken: id => ask('DELETE', '/api/tokens', {id}),
    // identities are the sign-in accounts linked to the caller: [{provider, issuer, subject, username?, email?, name?,
    // linked, last_login?}]; unlinkIdentity takes one off them (never the last: 409 last).
    identities: async () => (await ask('GET', '/api/identities')) || [],
    unlinkIdentity: ({provider, issuer, subject}) => ask('DELETE', '/api/identities', {provider, issuer, subject}),
    // webhook is the caller's personal webhook ('' for none); setWebhook changes it ('' removes it).
    webhook: async () => (await ask('GET', '/api/me/webhook'))?.url || '',
    setWebhook: url => ask('POST', '/api/me/webhook', {url}),
    // trackerAccounts are the trackers of the caller's projects, one per address, and who they are there: [{kind, base,
    // login?}].
    trackerAccounts: async () => (await ask('GET', '/api/me/trackers')) || [],
    // testWebhook posts a test to the saved webhook → {ok, status?}: the HTTP status it answered, or unreachable.
    testWebhook: () => ask('POST', '/api/me/webhook/test'),
    // presets are the built-in skins, the default first: [{name, input: {base, accent}}].
    presets: async () => (await ask('GET', '/theme/presets.json')) || [],
    // trackers are the tracker bindings of the projects the caller manages; bindTracker binds one
    // ({project, kind, base, repo, token, settings}) → the binding with its webhook secret, once.
    trackers: async () => (await ask('GET', '/api/trackers')) || [],
    bindTracker: p => ask('POST', '/api/trackers', p),
    unbindTracker: id => ask('DELETE', '/api/trackers', {id}),
    trackerSettings: (id, settings) => ask('POST', '/api/trackers/settings', {id, settings}),
    trackerToken: (id, token) => ask('POST', '/api/trackers/credential', {id, token}),
    rescanTracker: id => ask('POST', '/api/trackers/rescan', {id}),
    // trackerIssues is a binding's sync log; trackerPreview the progress comment issue number would get now → {body}.
    trackerIssues: async id => (await ask('GET', '/api/trackers/issues?id=' + encodeURIComponent(id))) || [],
    trackerPreview: (id, number) => ask('GET', `/api/trackers/preview?id=${encodeURIComponent(id)}&number=${number}`),
  };
}

// linkURL is where a sign-in method's button goes to link that account to the signed-in user.
export const linkURL = name => `/auth/${encodeURIComponent(name)}/start?link=1`;

// startURL is where a sign-in method's button goes; an invitation rides along.
export const startURL = (name, invite = '') => `/auth/${encodeURIComponent(name)}/start${invite ? '?invite=' + encodeURIComponent(invite) : ''}`;

// http is the page's plain HTTP: the browser session (/session, /login, /logout), the sign-in methods and invitation
// (/auth/*), a terminal's sign-in to allow (/api/device) and who is who (/api/users). Writes carry X-Tend, which the server asks of a browser.

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
    logout: () => ask('POST', '/logout', {}, true),
    // logins are the sign-in methods this server offers: [{name, display}].
    logins: async () => (await ask('GET', '/auth/logins')) || [],
    // invite is what an invitation link holds: {inviter, role, project?, access?, expires}.
    invite: code => ask('GET', '/auth/invite?code=' + encodeURIComponent(code)),
    // device is a terminal's pending sign-in: {code, name, ip, created}.
    device: code => ask('GET', '/api/device?code=' + encodeURIComponent(code)),
    decideDevice: (code, allow) => ask('POST', '/api/device', {code, allow}),
    // users are the people on this server: [{id, name, username, role, disabled}].
    users: async () => (await ask('GET', '/api/users')) || [],
  };
}

// startURL is where a sign-in method's button goes; an invitation rides along.
export const startURL = (name, invite = '') => `/auth/${encodeURIComponent(name)}/start${invite ? '?invite=' + encodeURIComponent(invite) : ''}`;

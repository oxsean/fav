// fake is the server end of the core tests: WebSocket-like sockets for wire.js and a player for the frame files in
// frames/. A frame file is JSON lines, played in order:
//
//	{"c": frame}      the client sent this frame next (compared whole)
//	{"s": frame}      the server sends this frame (one message, newline added)
//	{"raw": "text"}   the server sends this text as one message, as it is
//	{"connect": true} the socket the client is dialling opens
//	{"refuse": true}  the socket the client is dialling fails before it opens
//	{"drop": true}    the server ends the open connection
//	{"dialing": bool} whether the client is dialling a socket not yet opened or refused
//	{"wait_ms": n}    time passes on the fake clock
//	{"step": "name"}  the test acts as the page would (calls, watches, cancels)
//	{"note": "text"}  says what follows
//
// The Go tests of the wire streams and state.watch read the same files: c is what a client sends, s what the server
// answers; the lines about time, dialling and steps belong to the client and are theirs to skip.
import {readFileSync} from 'node:fs';
import {isDeepStrictEqual, inspect} from 'node:util';

export const settle = () => new Promise(r => setImmediate(r));

export function clock() {
  let now = 0, next = 1;
  const timers = new Map();
  return {
    setTimeout(fn, ms = 0) { const id = next++; timers.set(id, {at: now + ms, fn}); return id; },
    clearTimeout(id) { timers.delete(id); },
    now: () => now,
    async advance(ms) {
      const end = now + ms;
      for (;;) {
        let first = null;
        for (const [id, t] of timers) if (t.at <= end && (!first || t.at < first[1].at || t.at === first[1].at && id < first[0])) first = [id, t];
        if (!first) break;
        timers.delete(first[0]);
        now = first[1].at;
        first[1].fn();
        await settle();
      }
      now = end;
    },
  };
}

export function readFrames(name) {
  const text = readFileSync(new URL(`./frames/${name}.jsonl`, import.meta.url), 'utf8');
  return text.split('\n').map((l, i) => [i + 1, l.trim()]).filter(([, l]) => l).map(([n, l]) => {
    try { return {n, ...JSON.parse(l)}; } catch (e) { throw new Error(`${name}.jsonl:${n}: ${e.message}`); }
  });
}

export function server(clk) {
  const sockets = [];
  let current = null;

  function open(url) {
    const s = {
      url, state: 'dialing', sent: [], buf: '', onopen: null, onmessage: null, onclose: null,
      send(data) {
        if (s.state !== 'open') throw new Error(`send on a ${s.state} socket`);
        s.buf += data;
        let i;
        while ((i = s.buf.indexOf('\n')) >= 0) {
          const line = s.buf.slice(0, i);
          s.buf = s.buf.slice(i + 1);
          s.sent.push(JSON.parse(line));
        }
      },
      close() {
        if (s.state === 'closed') return;
        s.state = 'closed';
        setImmediate(() => s.onclose?.({}));
      },
    };
    sockets.push(s);
    return s;
  }

  const dialing = () => sockets.find(s => s.state === 'dialing');

  async function play(name, steps = {}) {
    for (const line of readFrames(name)) {
      await settle();
      const at = `${name}.jsonl:${line.n}`;
      if (line.note !== undefined) continue;
      if (line.connect || line.refuse) {
        const s = dialing();
        if (!s) throw new Error(`${at}: the client is not dialling`);
        if (line.refuse) { s.state = 'closed'; s.onclose?.({}); continue; }
        s.state = 'open';
        current = s;
        s.onopen?.({});
      } else if (line.drop) {
        if (!current || current.state !== 'open') throw new Error(`${at}: no open connection to drop`);
        current.state = 'closed';
        current.onclose?.({});
      } else if (line.dialing !== undefined) {
        if (!!dialing() !== line.dialing) throw new Error(`${at}: dialling is ${!!dialing()}`);
      } else if (line.wait_ms !== undefined) {
        await clk.advance(line.wait_ms);
      } else if (line.step !== undefined) {
        if (!steps[line.step]) throw new Error(`${at}: the test has no step ${line.step}`);
        await steps[line.step]();
      } else if (line.s !== undefined || line.raw !== undefined) {
        if (!current || current.state !== 'open') throw new Error(`${at}: no open connection to send on`);
        current.onmessage?.({data: line.raw !== undefined ? line.raw : JSON.stringify(line.s) + '\n'});
      } else if (line.c !== undefined) {
        const got = current?.sent.shift();
        if (!got) throw new Error(`${at}: the client sent nothing, want ${JSON.stringify(line.c)}`);
        if (!isDeepStrictEqual(got, line.c)) throw new Error(`${at}: the client sent ${JSON.stringify(got)}, want ${JSON.stringify(line.c)}`);
      } else throw new Error(`${at}: no such line ${inspect(line)}`);
    }
    await settle();
    for (const s of sockets) if (s.sent.length) throw new Error(`${name}: the client also sent ${JSON.stringify(s.sent)}`);
  }

  return {open, play, sockets, current: () => current};
}

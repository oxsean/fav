// check is the core tests' harness: cases run in order and the result goes to stdout as JSON, one entry per case,
// for the Go test that runs the file (runModule) to report as subtests. What a case returns goes along as its data, for
// the Go test to compare with its own.
import {isDeepStrictEqual, inspect} from 'node:util';
import {act} from './vendor/test-utils.mjs';

const cases = [];

export const test = (name, fn) => cases.push({name, fn});

export function eq(got, want, what = 'value') {
  if (!isDeepStrictEqual(got, want)) throw new Error(`${what}: got ${inspect(got, {depth: 8})}, want ${inspect(want, {depth: 8})}`);
}

export function ok(cond, what) {
  if (!cond) throw new Error(what);
}

// until waits in real time, in short acts, for what preact's deferred effects do: after a render outside act they run on
// a 35 ms timer, later on a loaded machine.
export async function until(cond, what, ms = 5000) {
  for (const end = Date.now() + ms; !cond();) {
    if (Date.now() > end) throw new Error(`waited ${ms} ms for ${what}`);
    await act(() => new Promise(res => setTimeout(res, 5)));
  }
}

export async function throws(fn, pattern, what = 'call') {
  try { await fn(); } catch (e) {
    if (pattern && !pattern.test(String(e?.message ?? e))) throw new Error(`${what} threw ${e?.message}, want ${pattern}`);
    return e;
  }
  throw new Error(`${what} did not throw`);
}

export async function run() {
  const out = [];
  for (const c of cases) {
    try {
      const data = await c.fn();
      out.push(data === undefined ? {name: c.name} : {name: c.name, data});
    } catch (e) {
      out.push({name: c.name, error: String(e?.stack ?? e)});
    }
  }
  process.stdout.write(JSON.stringify(out));
}

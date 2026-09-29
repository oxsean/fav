// check is the core tests' harness: cases run in order and the result goes to stdout as JSON, one entry per case,
// for the Go test that runs the file (runModule) to report as subtests. What a case returns goes along as its data, for
// the Go test to compare with its own.
import {isDeepStrictEqual, inspect} from 'node:util';

const cases = [];

export const test = (name, fn) => cases.push({name, fn});

export function eq(got, want, what = 'value') {
  if (!isDeepStrictEqual(got, want)) throw new Error(`${what}: got ${inspect(got, {depth: 8})}, want ${inspect(want, {depth: 8})}`);
}

export function ok(cond, what) {
  if (!cond) throw new Error(what);
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

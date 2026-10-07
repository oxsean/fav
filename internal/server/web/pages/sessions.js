// sessions is the Claude / Codex sessions of every machine the viewer reads (?page=sessions), one list whatever machine
// they are on: the query box is its one state (&q=, the TUI's syntax; Go reads it, the page only drops and adds whole
// tokens), the chips under it its other spelling (machine, project, tag, source, state, time, favorites only (&fav=1),
// order (&sort=)), and one session open (&open=<machine>/<provider>:<id>) beside the list, or over it on a phone. The
// viewer's own sessions change in place (favorite, done, archive, edit; each taken back with undo), become tasks, and
// go into their machine's trash (confirmed; undo restores), which the state trash lists and restores from; a machine
// shared with them, or an older tend, is read only. A query starting with > searches the messages of every
// machine (on Enter) and opens a session on its best hit. Machines that did not answer say so under the chips. Nothing
// is polled: the list is read when it opens, again when a machine says its records changed (records_rev), when what
// the viewer may see changes (the coordinator's reset), when the page shows again, after a reconnect, and on Refresh.
import {useState, useEffect, useRef, useMemo} from '../vendor/hooks.mjs';
import {html, cx, usePhone, useWords, useSignalValue, useName, useActions, useKeys} from '../ui/base.js';
import {Picker} from '../ui/picker.js';
import {Menu} from '../ui/menu.js';
import {Button, Chip, Segmented, Kbd, arrowStep} from '../ui/controls.js';
import {Icon} from '../ui/icons.js';
import {Table} from '../ui/table.js';
import {Status} from '../ui/status.js';
import {Drawer, Modal, focusables} from '../ui/overlay.js';
import {register} from '../core/i18n.js';
import {duration, clock, day} from '../core/format.js';
import * as ss from '../core/sessions.js';
import {code} from '../core/proto.js';
import {projectsOf} from '../core/team.js';
import {apiText} from './words.js';
import {Conversation, EditSession, MakeTask, DeleteSession, TaskLabel, Star, Archived, tagChip, Marked, makeWhy, when} from './sessionview.js';

register('sessions', {
  'sess.pageTitle': ['会话', 'Sessions'],
  'sess.counts': ['共 %d 个 · 筛出 %d 个 · 在跑 %d 个', '%d {session|sessions} · %d shown · %d running'],
  'sess.queryHint': ['筛选：词、#标签、project:、host:、status:、last:7d … 以 > 开头搜消息', 'Filter: words, #tag, project:, host:, status:, last:7d … start with > to search messages'],
  'sess.syntax': ['查询语法', 'Query syntax'], 'sess.unknown': ['不认识的写法：%s（当普通词搜了）', 'Not understood: %s (searched as plain words)'],
  'sess.filter': ['筛选会话', 'Filter the sessions'], 'sess.doneLabel': ['完成 / 取消完成', 'Done / not done'], 'sess.msgSearch': ['搜消息', 'Search messages'],
  'sess.f.machine': ['机器', 'Machine'], 'sess.f.machineAll': ['全部 · %d 台', 'All · %d {machine|machines}'], 'sess.f.project': ['项目', 'Project'],
  'sess.f.tag': ['标签', 'Tag'], 'sess.f.source': ['来源', 'Source'], 'sess.f.state': ['状态', 'State'], 'sess.f.time': ['时间', 'Time'], 'sess.f.sort': ['排序', 'Sort'],
  'sess.f.any': ['全部', 'Any'], 'sess.f.anyTime': ['不限', 'Any time'], 'sess.favOnly': ['只看收藏', 'Favorites only'],
  'sess.st.open': ['未归档', 'Unarchived'], 'sess.st.active': ['进行中', 'In progress'], 'sess.st.done': ['已完成', 'Done'], 'sess.st.archived': ['已归档', 'Archived'], 'sess.st.all': ['全部', 'All'],
  'sess.st.trash': ['回收站', 'Trash'],
  'sess.sort.active': ['最近活动', 'Last active'], 'sess.sort.started': ['开始', 'Started'], 'sess.sort.favorited': ['收藏', 'Favorited'], 'sess.sort.turns': ['轮数', 'Turns'],
  'sess.t.today': ['今天', 'Today'], 'sess.t.7d': ['最近 7 天', 'Last 7 days'], 'sess.t.30d': ['最近 30 天', 'Last 30 days'], 'sess.t.year': ['今年', 'This year'],
  'sess.projectNone': ['未归项目', 'No project'],
  'sess.c.state': ['状态', 'State'], 'sess.c.title': ['标题', 'Title'], 'sess.c.machine': ['机器', 'Machine'], 'sess.c.project': ['项目', 'Project'],
  'sess.c.agent': ['Agent', 'Agent'], 'sess.c.last': ['最近活动', 'Last active'], 'sess.c.hits': ['命中', 'Hits'], 'sess.c.deleted': ['删除时间', 'Deleted'],
  'sess.deletedAt': ['删于 %s', 'Deleted %s'], 'sess.trashHead': ['回收站 · %d 个', 'Trash · %d'],
  'sess.ago': ['%s 前', '%s ago'],
  'sess.m.offline': ['%s 离线 · %s：没列它的会话', '%s is offline · %s: its sessions are not listed'],
  'sess.m.timeout': ['%s 没有及时回答（%d 秒）：没列它的会话', '%s did not answer in time (%d s): its sessions are not listed'],
  'sess.m.retry': ['再试', 'Retry'],
  'sess.m.old': ['%s 的 tend 是旧版（%s）：能看，不能改，也没搜它。在那台机器上更新 tend：tend hosts install %s',
    '%s runs an older tend (%s): readable, not changeable, not searched. Update it there: tend hosts install %s'],
  'sess.m.oldShort': ['%s 是旧版：能看，不能改', '%s is outdated: read only'],
  'sess.m.oldTrash': ['%s 是旧版：回收站要在那台机器的 TUI 里看', '%s is outdated: see its trash in its TUI'],
  'sess.m.unauthorized': ['%s 的节点不让读这些会话', 'The node on %s does not let these sessions be read'],
  'sess.m.error': ['%s 没有回答：%s', '%s did not answer: %s'],
  'sess.m.shared': ['%s 是 %s 共享给你的：能看全部会话，不能改', '%s is shared with you by %s: you can read every session but not change them'],
  'sess.m.sharedRuns': ['%s 只共享运行产生的会话：这里只列出那些，%s 自己开的会话不列', '%s shares only sessions its runs made: those are listed, not the ones %s opened'],
  'sess.m.sharedNone': ['%s 不放出任何会话。这是那台机器自己的设置，只有主人能改。', '%s releases no sessions. That is the machine\'s own setting; only its owner changes it.'],
  'sess.m.sharedSub': ['%s 共享 · 只读', 'Shared by %s · read only'],
  'sess.shareRuns': ['%s 只共享 tend 运行的会话，你在那台机器上手开的不在这里。要看全部：在它的 tend config.json 的 "node" 里写 "share_sessions": "all"，再重启它的 tend node。',
    '%s shares only the sessions of tend runs, so those opened there by hand are not here. To see them all, set "share_sessions": "all" under "node" in its tend config.json and restart its tend node.'],
  'sess.shareNone': ['%s 不共享任何会话。要看全部：在它的 tend config.json 的 "node" 里写 "share_sessions": "all"，再重启它的 tend node。',
    '%s shares no sessions. To see them all, set "share_sessions": "all" under "node" in its tend config.json and restart its tend node.'],
  'sess.m.building': ['%s 第一次搜消息，正文库在建（%d / %d）：先列出建好的部分，建完再搜一次会更全', '%s is building its message index (%d of %d): showing what is ready; search again once it is done'],
  'sess.m.searchAgain': ['再搜一次', 'Search again'],
  'sess.m.oldNoSearch': ['%s 的 tend 是旧版（%s）：没搜它。在那台机器上更新：tend hosts install %s', '%s runs an older tend (%s): not searched. Update it: tend hosts install %s'],
  'sess.msgHead': ['消息 · %d 个会话 · %d 处', 'Messages · %d {session|sessions} · %d {hit|hits}'], 'sess.hitsAt': ['%d 处 · %s', '%d {hit|hits} · %s'],
  'sess.fixes': ['也搜了 %s（你写的是 %s）', 'Also searched %s (you typed %s)'],
  'sess.tooLong': ['关键词太多：最多 %d 个，删掉几个再搜', 'Too many words: at most %d, remove some'],
  'sess.noHits': ['没搜到', 'Nothing found'], 'sess.noHitsSub': ['这些机器的消息里都没有「%s」。少写一个词再试', 'No message on these machines has “%s”. Try fewer words'],
  'sess.msgIdle': ['输入关键词，按 Enter 搜所有机器上的消息', 'Type words and press Enter to search messages on every machine'],
  'sess.sharedBy': ['%s 共享', 'Shared by %s'], 'sess.sharedRO': ['%s 共享，只读', 'Shared by %s, read only'],
  'sess.readOnlyKey': ['只读：%s 的 %s 共享给你，只有主人能改', 'Read only: %s shares %s with you; only the owner can change it'],
  'sess.roOther': ['这条会话不能在这里改', 'This session cannot be changed here'],
  'sess.toast.fav': ['已收藏「%s」', 'Favorited “%s”'], 'sess.toast.unfav': ['已取消收藏「%s」', 'Unfavorited “%s”'],
  'sess.toast.done': ['「%s」标为完成', 'Marked “%s” done'], 'sess.toast.undone': ['「%s」回到进行中', '“%s” is in progress again'],
  'sess.toast.archived': ['已归档「%s」', 'Archived “%s”'], 'sess.toast.unarchived': ['已取消归档「%s」', 'Unarchived “%s”'],
  'sess.toast.edited': ['已保存「%s」', 'Saved “%s”'],
  'sess.deleted': ['已把「%s」移到 %s 的回收站', 'Moved “%s” to the trash on %s'], 'sess.delBusy': ['会话在跑，不能删', 'The session is running and cannot be deleted'],
  'sess.delOld': ['%s 的 tend 没有回收站，不能在这里删。在那台机器上更新 tend：tend hosts install %s', '%s runs a tend without a trash, so it cannot delete here. Update it: tend hosts install %s'],
  'sess.restored': ['已还原「%s」', 'Restored “%s”'],
  'sess.made': ['已建成任务，在 %s 上接着做', 'Task made; it goes on on %s'], 'sess.madeGo': ['去看', 'Open'],
  'sess.madePrivate': ['已建成任务 %s，但 server 不认项目：它是私人的', 'Made task %s, but the server ignores projects: it is private'],
  'sess.e.noMachine': ['还没有能看会话的机器', 'No machine whose sessions you can read'],
  'sess.e.noMachineSub': ['会话在各台机器上；先在机器页添加一台，或请别人把机器共享给你', 'Sessions live on machines: add one on the machines page, or ask someone to share theirs'],
  'sess.e.addMachine': ['去机器页添加一台', 'Add a machine'], 'sess.e.none': ['没有符合的会话', 'No session matches'],
  'sess.e.noneSub': ['生效的条件，点掉一个再看：', 'Filters in force; remove one:'], 'sess.e.noFav': ['没有收藏的会话', 'No favorite sessions'],
  'sess.e.noFavSub': ['在会话上按 f 收藏，或在 TUI、/tend 里收藏', 'Press f on a session, or favorite it in the TUI or with /tend'],
  'sess.e.allSessions': ['看全部会话', 'Show all sessions'], 'sess.e.agent': ['一次性 agent 会话只在 TUI 里看', 'One-off agent sessions are listed in the TUI only'],
  'sess.e.agentSub': ['status:agent 是 TUI 的视图；网页上换一个状态', 'status:agent is a TUI view; pick another state here'], 'sess.e.open': ['看未归档的会话', 'Show unarchived sessions'],
  'sess.e.trash': ['回收站是空的', 'The trash is empty'],
  'sess.e.trashSub': ['删掉的会话在这里留 %d 天，之后自动清掉；要马上清空，在那台机器上运行 tend trash --purge',
    'Deleted sessions stay %d {day|days}, then go; to empty it now run tend trash --purge on that machine'],
  'sess.e.trashKeep': ['删掉的会话在这里能还原；要清空，在那台机器上运行 tend trash --purge', 'Deleted sessions can be restored from here; to empty it run tend trash --purge on that machine'],
  'sess.dropToken': ['去掉 %s', 'Remove %s'], 'sess.pick': ['选一个会话看对话', 'Pick a session to read it'],
  'sess.filtersPage': ['筛选', 'Filters'], 'sess.moreFilters': ['更多筛选', 'More filters'], 'sess.showN': ['看 %d 个会话', 'Show %d {session|sessions}'], 'sess.clear': ['清空筛选', 'Clear filters'],
  'sess.syn.word': ['词', 'word'], 'sess.syn.tag': ['#标签', '#tag'],
  'sess.syn.words': ['标题、摘要、目录、分支、标签里有它；几个词都要有', 'In the title, summary, directory, branch or tags; every word must be'],
  'sess.syn.tags': ['有这个标签；几个都要有', 'Has the tag; every one given'],
  'sess.syn.project': ['在这个项目里（名字或 id）；project:none 是不在项目里的', 'In this project (name or id); project:none is in none'],
  'sess.syn.host': ['只看这台机器', 'Only this machine'],
  'sess.syn.status': ['open（默认，未归档）· active · todo · doing · done · archived · all · trash（回收站）', 'open (the default: unarchived) · active · todo · doing · done · archived · all · trash'],
  'sess.syn.provider': ['claude 或 codex', 'claude or codex'],
  'sess.syn.last': ['最近活动在这之后：7d、2w、09-01、2026-09-01', 'Active since: 7d, 2w, 09-01, 2026-09-01'],
  'sess.syn.when': ['在这之后 / 之前开始', 'Started after / before'],
  'sess.syn.turns': ['没收藏的至少 N 轮才列，默认 3', 'Sessions not favorited need N turns; 3 by default'],
  'sess.syn.file': ['AI 写过路径含它的文件', 'The AI wrote a file whose path holds it'],
  'sess.syn.grep': ['搜消息，按 Enter 搜所有机器；后面也能写上面的筛选', 'Search messages on every machine with Enter; the filters above go after it too'],
  'sess.syn.grepWords': ['（> 之后）原样出现 · 二选一 · 不含 · 只看自己说的', '(after >) as written · either · without · only what you said'],
});

// ⚠️ How long the coordinator waits for a machine (coord.machineWait); how long typing rests before the query goes
// out; the least time between two reads a machine's records_rev asks for.
const MACHINE_WAIT_S = 5;
const TYPING_MS = 250;
const AGAIN_MS = 1000;

const syntax = [
  ['sess.syn.word', 'sess.syn.words'], ['sess.syn.tag', 'sess.syn.tags'], ['project:x  p:x', 'sess.syn.project'], ['host:x  machine:x', 'sess.syn.host'],
  ['status:x', 'sess.syn.status'], ['provider:x  source:x', 'sess.syn.provider'], ['last:7d', 'sess.syn.last'], ['after:  before:', 'sess.syn.when'],
  ['turns:N', 'sess.syn.turns'], ['file:x', 'sess.syn.file'], ['> …', 'sess.syn.grep'], ['"…"  a|b  -x  who:me', 'sess.syn.grepWords'],
];

// ⚠️ A list row's height in px, as css/pages.css fixes it (.sess-list .tr; .sess-phone .card-row plus the 1px line
// between cards): the table draws a long list in windows of it.
export const ROW = {desktop: 50, phone: 100};

// deletedWhen is when a session was deleted as the trash lists it: the clock today, else the date alone.
const deletedWhen = (at, now) => {
  const t = Date.parse(at || '');
  return !t ? '' : day(t) === day(now) ? clock(t) : day(t);
};

const ownerOf = (session, m) => (m?.owner && m.owner !== session?.id ? m.owner : '');

// Syntax is the query's syntax, as docs/design/sessions/index-and-search.md writes it.
function Syntax({onClose}) {
  const {t} = useWords();
  useKeys('modal', [{key: 'Esc', run: onClose}], {blocks: true});
  return html`<div class="menu-scrim" onClick=${onClose}></div><div class="sv-pop sv-syntax" role="dialog" aria-label=${t('sess.syntax')}>
    <h3>${t('sess.syntax')}</h3>
    <table><tbody>${syntax.map(([k, v]) => html`<tr key=${k}><td>${k.startsWith('sess.') ? t(k) : k}</td><td>${t(v)}</td></tr>`)}</tbody></table>
  </div>`;
}

// MachineLines say, one line each, what of a machine is not listed or not changeable, and why.
function MachineLines({lines}) {
  if (!lines.length) return null;
  return html`<div class="sv-machines">${lines.map(l => html`<p class=${cx('sv-mline', l.tone)} key=${l.key}>
    ${l.icon}<span class="msg">${l.text}</span>${l.act && html`<${Button} kind="quiet" onClick=${l.act.run}>${l.act.label}<//>`}</p>`)}</div>`;
}

// RowMenu is a row's ⋯: its list over the page where the button was, since a row clips what overflows it.
function RowMenu({at, items, onClose}) {
  const box = useRef(null);
  useKeys('modal', [{key: 'Esc', run: onClose}], {blocks: true});
  useEffect(() => { focusables(box.current)[0]?.focus?.(); }, []);
  const onKeyDown = e => {
    const list = focusables(box.current);
    const j = arrowStep(e.key, list.indexOf(box.current.ownerDocument.activeElement), list.length);
    if (j !== null && list.length) { e.preventDefault(); list[j].focus(); }
  };
  return html`<div class="menu-scrim" onClick=${onClose}></div>
    <div class="menu sv-rowmenu" role="menu" ref=${box} onKeyDown=${onKeyDown} style=${{top: at.y + 'px', left: at.x + 'px'}}>
      ${items.map(x => html`<button type="button" role="menuitem" class=${cx('menu-item', x.kind)} onClick=${() => { onClose(); x.onClick(); }}>${x.label}<${Kbd} k=${x.keyName} /></button>`)}
    </div>`;
}

// Sessions: route is the address's part ({q, fav, sort, open}); session who the viewer is; commands sends run.continue;
// timers wait out typing and too many records_rev; doc tells when the page shows again; limit is a page's rows; copy
// is the clipboard's (a test passes its own).
export function Sessions({store, wire, router, toasts, commands, session = null, route = {}, copy, clock: now = () => Date.now(), timers = globalThis,
  doc = null, limit = ss.LIMIT}) {
  const w = useWords();
  const {t, f} = w;
  const phone = usePhone();
  const userName = useName();
  const machines = useSignalValue(store.machines);
  const status = useSignalValue(wire.status);
  useSignalValue(store.rev.projects);
  const people = useMemo(() => ss.createPeople({wire, phase: store.phase}), [wire]);
  const names = useSignalValue(people.names);
  const name = id => names[id] || userName(id);
  const q = route.q || '', fav = !!route.fav, sort = route.sort || '', open = route.open || '';
  const all = !fav, search = ss.searching(q);
  const at = now();

  const [text, setText] = useState(q);
  const [list, setList] = useState(null);
  const [found, setFound] = useState(null);
  const [pending, setPending] = useState(new Set());
  const [modal, setModal] = useState(null);
  const [syntaxOpen, setSyntaxOpen] = useState(false);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [want, setWant] = useState({machine: 0, tags: 0});
  const [grepRev, setGrepRev] = useState(0);
  const seq = useRef(0), grepSeq = useRef(0), more = useRef(false), typing = useRef(null), again = useRef(null), revs = useRef({}), live = useRef(null);
  const input = useRef(null), chips = useRef(null);

  const here = o => {
    const r = {page: 'sessions', q, fav, sort, open, ...o};
    for (const k of ['q', 'sort', 'open']) if (!r[k]) delete r[k];
    if (!r.fav) delete r.fav;
    return r;
  };
  const go = (o, replace = true) => router.go(here(o), {replace});
  const setQ = v => go({q: v.trim()});
  useEffect(() => { if (q !== text.trim()) setText(q); }, [q]);

  const openNow = useRef(open), listNow = useRef(null);
  openNow.current = open;
  listNow.current = list;

  // load reads the list from the top; keep reads as many rows as are listed (a record changed, the page came back); one
  // that goes into the trash view or out of it closes, in place, a conversation it no longer lists.
  const load = ({keep = false, fresh = false} = {}) => {
    if (search) return;
    const n = ++seq.current;
    more.current = false;
    const shown = keep ? Math.min(ss.MAX, Math.max(limit, list?.rows?.length || 0)) : limit;
    if (!keep) setList(o => (o ? {...o, loading: true} : null));
    ss.query(wire, {q, all, sort, limit: shown, fresh}).then(a => {
      if (n !== seq.current) return;
      const trashed = l => ss.chosen(l?.tokens).status?.value === ss.TRASH;
      const listed = (a.rows || []).some(r => ss.keyOf(r) === openNow.current);
      if (openNow.current && !listed && listNow.current && trashed(listNow.current) !== trashed(a)) go({open: ''});
      setList({...a, rows: a.rows || [], machines: a.machines || [], loading: false, error: null});
      people.ask((a.machines || []).map(m => m.owner).filter(Boolean));
    }, e => n === seq.current && setList(o => ({...(o || {rows: [], machines: [], tokens: []}), loading: false, error: e})));
  };
  live.current = load;
  useEffect(() => { load(); }, [q, all, sort, search]);
  const nextPage = () => {
    if (search || !list?.next || more.current) return;
    more.current = true;
    const n = seq.current;
    ss.query(wire, {q, all, sort, limit, after: list.next}).then(a => {
      more.current = false;
      if (n !== seq.current) return;
      setList(o => ({...a, rows: [...(o?.rows || []), ...(a.rows || [])], machines: a.machines || [], loading: false, error: null}));
    }, () => { more.current = false; });
  };

  // ⚠️ records_rev rises when a machine's records change elsewhere: read the list again, once a second at most.
  useEffect(() => {
    const seen = revs.current, named = new Set((list?.machines || []).map(m => m.name));
    let rose = false;
    for (const m of machines) {
      if (seen[m.name] !== undefined && (m.records_rev || 0) > seen[m.name] && named.has(m.name)) rose = true;
      seen[m.name] = m.records_rev || 0;
    }
    if (!rose || again.current) return;
    again.current = timers.setTimeout(() => { again.current = null; live.current({keep: true}); }, AGAIN_MS);
  }, [machines]);
  useEffect(() => () => { timers.clearTimeout(again.current); timers.clearTimeout(typing.current); }, []);
  // A reset says what the viewer may see changed (a machine's sessions shared or no longer): ask again.
  const resets = useSignalValue(store.resets);
  const resetsSeen = useRef(resets);
  useEffect(() => {
    if (resets === resetsSeen.current) return;
    resetsSeen.current = resets;
    if (!search) live.current({keep: true});
    else if (ss.searchText(q).trim()) setGrepRev(n => n + 1);
  }, [resets]);
  const wasOpen = useRef(status === 'open');
  useEffect(() => {
    if (status === 'open' && !wasOpen.current) live.current({keep: true});
    wasOpen.current = status === 'open';
  }, [status]);
  useEffect(() => {
    if (!doc?.addEventListener) return;
    const shown = () => { if (doc.visibilityState !== 'hidden') live.current({keep: true}); };
    doc.addEventListener('visibilitychange', shown);
    return () => doc.removeEventListener('visibilitychange', shown);
  }, [doc]);

  // Message search goes out on Enter only: it asks every machine.
  useEffect(() => {
    if (!search || !ss.searchText(q).trim()) { setFound(null); return; }
    const n = ++grepSeq.current;
    setFound(o => (o ? {...o, loading: true} : {hits: [], machines: [], loading: true}));
    ss.grep(wire, {q, all}).then(a => {
      if (n !== grepSeq.current) return;
      setFound({...a, hits: a.hits || [], machines: a.machines || [], loading: false});
      people.ask((a.machines || []).map(m => m.owner).filter(Boolean));
    }, e => n === grepSeq.current && setFound({hits: [], machines: [], loading: false, error: e}));
  }, [q, all, search, grepRev]);

  const answers = (search ? found?.machines : list?.machines) || [];
  const answerOf = m => answers.find(x => x.name === m);
  const keyOfHit = h => ss.keyOf({...h.row, machine: h.machine});
  const hitOf = search && open ? (found?.hits || []).find(h => keyOfHit(h) === open) : null;
  const listed = (list?.rows || []).find(r => ss.keyOf(r) === open);
  const row = listed || (hitOf ? {...hitOf.row, machine: hitOf.machine, writable: !!answerOf(hitOf.machine)?.writable, ...(hitOf.make ? {make: hitOf.make} : {})} : null);
  const cur = row;
  const tokens = list?.tokens || [];
  const c = ss.chosen(tokens);
  const inTrash = !search && c.status?.value === ss.TRASH;
  const readable = machines.filter(m => !m.retired && m.sessions).sort((a, b) => a.name.localeCompare(b.name));

  // A row's record changes: at once in the list, then as its machine answers; undo puts back what it had.
  const replace = (key, fn) => setList(o => (o ? {...o, rows: o.rows.map(r => (ss.keyOf(r) === key ? fn(r) : r))} : o));
  const busy = (key, on) => setPending(p => { const n = new Set(p); if (on) n.add(key); else n.delete(key); return n; });
  const rowNow = key => (listNow.current?.rows || []).find(r => ss.keyOf(r) === key);
  const send = (r, patch, expect = '') => {
    const key = ss.keyOf(r);
    replace(key, x => ss.applied(x, patch, now()));
    busy(key, true);
    return ss.put(wire, r, patch, expect).then(got => {
      busy(key, false);
      replace(key, x => ss.merged(x, got));
      return got;
    }, e => {
      busy(key, false);
      replace(key, () => r);
      throw e;
    });
  };
  const toastOf = {
    favorite: r => (ss.favorited(r) ? 'sess.toast.unfav' : 'sess.toast.fav'),
    archive: r => (r.archived_at ? 'sess.toast.unarchived' : 'sess.toast.archived'),
    done: r => (r.status === 'done' ? 'sess.toast.undone' : 'sess.toast.done'),
  };
  const roWhy = r => {
    const a = answerOf(r.machine), owner = ownerOf(session, a);
    if (owner) return f('sess.readOnlyKey', name(owner), r.machine);
    if (a?.state === ss.answer.old) return f('sess.m.oldShort', r.machine);
    return t('sess.roOther');
  };
  const flip = (r, kind) => {
    if (!r) return;
    if (!r.writable) { toasts.show({text: roWhy(r)}); return; }
    const patch = ss.toggles[kind](r);
    const title = r.title || t('sess.untitled');
    send(r, patch).then(() => toasts.show({text: f(toastOf[kind](r), title), undo: () => {
      const back = rowNow(ss.keyOf(r));
      if (back) send(back, ss.undoOf(r, patch)).catch(e => toasts.show({text: apiText(w, e), tone: 'danger'}));
    }}), e => toasts.show({text: apiText(w, e), tone: 'danger'}));
  };
  const edit = r => {
    if (!r) return;
    if (!r.writable) { toasts.show({text: roWhy(r)}); return; }
    setModal({kind: 'edit', key: ss.keyOf(r), base: r, stale: false});
  };
  // reread finds a row's record as it is now, for an edit that met stale: its machine's sessions, every state.
  const reread = r => ss.query(wire, {q: `host:${r.machine} status:all turns:0`, all: true, limit: ss.MAX})
    .then(a => (a.rows || []).find(x => ss.keyOf(x) === ss.keyOf(r)) || null);
  const saveEdit = (patch, expect) => {
    const base = modal.base;
    return send(base, patch, expect).then(got => {
      setModal(null);
      toasts.show({text: f('sess.toast.edited', got.title || base.title), undo: () => {
        const back = rowNow(ss.keyOf(base));
        if (back) send(back, ss.undoOf(base, patch)).catch(e => toasts.show({text: apiText(w, e), tone: 'danger'}));
      }});
    }, e => {
      if (e?.code !== code.stale) { toasts.show({text: apiText(w, e), tone: 'danger'}); return; }
      return reread(base).then(now => {
        if (!now) { setModal(null); toasts.show({text: t('sess.gone'), tone: 'danger'}); return; }
        replace(ss.keyOf(base), () => now);
        setModal(m => (m?.kind === 'edit' ? {...m, base: now, stale: true} : m));
      });
    });
  };
  const online = status === 'open';
  const canMake = r => !!r?.make && !!r.writable;
  const makeTask = r => { if (canMake(r)) setModal({kind: 'make', key: ss.keyOf(r), base: r}); };
  const makeProjects = projectsOf(store.state, session?.id || '').filter(p => p.role !== 'reader');
  const sendMake = ({text, agent, project}) => {
    const r = modal.base;
    const params = {machine: r.machine, provider: r.provider, session: r.session_id, dir: r.cwd || '', title: r.title || '', text, agent, project};
    for (const k of ['dir', 'title', 'project']) if (!params[k]) delete params[k];
    return commands.send('run.continue', params, {key: 'make:' + ss.keyOf(r)}).then(run => {
      setModal(null);
      const go = run?.task ? {label: t('sess.madeGo'), run: () => router.go({page: 'tasks', task: run.task})} : null;
      if (project && !run?.project) toasts.show({text: f('sess.madePrivate', run?.task || ''), act: go});
      else toasts.show({text: f('sess.made', r.machine), act: go});
    }, e => toasts.show({text: apiText(w, e), tone: 'danger'}));
  };

  // A session deleted or restored leaves the list at once, its machine's counts with it, and its conversation closes.
  const gone = r => {
    const key = ss.keyOf(r);
    if (openNow.current === key) { if (phone) router.back(here({open: ''})); else go({open: ''}); }
    setList(o => (o ? {...o, rows: o.rows.filter(x => ss.keyOf(x) !== key),
      machines: o.machines.map(m => (m.name === r.machine ? {...m, total: Math.max(0, m.total - 1), matched: Math.max(0, m.matched - 1)} : m))} : o));
  };
  const titleOf = (r, got) => r.title || got?.title || t('sess.untitled');
  const askTrash = r => {
    if (!r) return;
    const a = answerOf(r.machine);
    if (!ss.deletable(r, a)) { toasts.show({text: r.writable ? f('sess.delOld', r.machine, r.machine) : roWhy(r)}); return; }
    setModal({kind: 'trash', key: ss.keyOf(r), base: r, days: a.trash_days || 0});
  };
  const sendTrash = () => {
    const r = modal.base, key = ss.keyOf(r);
    setModal(null);
    busy(key, true);
    ss.trash(wire, r).then(got => {
      busy(key, false);
      gone(r);
      toasts.show({text: f('sess.deleted', titleOf(r, got), r.machine), undo: () => ss.restore(wire, r).then(() => live.current({keep: true}),
        e => toasts.show({text: apiText(w, e), tone: 'danger'}))});
    }, e => {
      busy(key, false);
      toasts.show({text: e?.code === code.busy ? t('sess.delBusy') : apiText(w, e), tone: 'danger'});
    });
  };
  const restoreRow = r => {
    if (!r) return;
    if (!ss.restorable(r, answerOf(r.machine))) { toasts.show({text: f('sess.m.oldTrash', r.machine)}); return; }
    const key = ss.keyOf(r);
    busy(key, true);
    ss.restore(wire, r).then(got => {
      busy(key, false);
      gone(r);
      toasts.show({text: f('sess.restored', titleOf(r, got))});
    }, e => {
      busy(key, false);
      toasts.show({text: apiText(w, e), tone: 'danger'});
    });
  };

  const focusQuery = () => input.current?.focus?.();
  const toSearch = () => {
    const v = search ? ss.searchText(text) : '> ' + text.trim();
    setText(v);
    if (search) setQ(v);
    focusQuery();
  };
  const cycleState = () => {
    const cycle = ss.states.filter(s => s !== ss.TRASH);
    const i = cycle.indexOf(ss.stateOf(tokens));
    setQ(ss.replaced(q, tokens, [ss.kind.status], ss.stateToken(cycle[(i + 1) % cycle.length])));
  };
  const toChips = () => focusables(chips.current)[0]?.focus?.();
  const rowWrite = () => !!cur && !inTrash;
  useActions('page', {
    search: {run: focusQuery, label: 'sess.filter'},
    msgSearch: {run: toSearch},
    filters: {run: toChips},
    machine: {run: () => setWant(o => ({...o, machine: o.machine + 1})), when: () => !search},
    tags: {run: () => setWant(o => ({...o, tags: o.tags + 1})), when: () => !search},
    state: {run: cycleState, when: () => !search},
    favorite: {run: () => flip(cur, 'favorite'), when: rowWrite},
    archive: {run: () => flip(cur, 'archive'), when: rowWrite},
    done: {run: () => flip(cur, 'done'), when: rowWrite, label: 'sess.doneLabel'},
    edit: {run: () => edit(cur), when: rowWrite},
    trash: {run: () => (inTrash ? restoreRow(cur) : askTrash(cur)), when: () => !!cur},
    makeTask: {run: () => makeTask(cur), when: () => canMake(cur)},
  }, {active: !modal && !filtersOpen});

  const onType = v => {
    setText(v);
    timers.clearTimeout(typing.current);
    if (ss.searching(v)) return;
    typing.current = timers.setTimeout(() => setQ(v), TYPING_MS);
  };
  const onQueryKey = e => {
    if (e.key !== 'Enter' || e.isComposing) return;
    e.preventDefault();
    timers.clearTimeout(typing.current);
    if (text.trim() === q) setGrepRev(n => n + 1);
    setQ(text);
  };
  const projectName = id => (!id ? t('sess.projectNone') : store.state.projects?.[id]?.name || id);
  const openRow = (key, push) => go({open: key}, !push);
  const selected = open;

  // The machine lines: what each machine that answered left out, and why.
  const lines = [];
  for (const a of answers) {
    const owner = ownerOf(session, a), m = machines.find(x => x.name === a.name);
    const line = (tone, icon, text, act) => lines.push({key: a.name + ':' + lines.length, tone, icon, text, act});
    if (a.state === ss.answer.offline) {
      const since = Date.parse(a.since || m?.last_seen || '');
      line('bad', html`<${Status} state="offline" />`, f('sess.m.offline', a.name, since ? f('sess.ago', duration(Math.max(60e3, at - since))) : '—'));
    } else if (a.state === ss.answer.timeout) {
      line('warn', html`<${Status} state="waiting" />`, f('sess.m.timeout', a.name, MACHINE_WAIT_S), {label: t('sess.m.retry'), run: () => (search ? setGrepRev(n => n + 1) : load({keep: true}))});
    } else if (a.state === ss.answer.old) {
      line('warn', html`<${Status} state="waiting" />`, inTrash ? f('sess.m.oldTrash', a.name) : phone ? f('sess.m.oldShort', a.name)
        : f(search ? 'sess.m.oldNoSearch' : 'sess.m.old', a.name, m?.version || '—', a.name));
    } else if (a.state === ss.answer.unauthorized) {
      line('bad', html`<${Status} state="failed" />`, f('sess.m.unauthorized', a.name));
    } else if (a.state === ss.answer.error) {
      line('bad', html`<${Status} state="failed" />`, f('sess.m.error', a.name, a.error || ''));
    } else {
      if (a.building) line('warn', html`<${Icon} name="search" size=${14} />`, f('sess.m.building', a.name, a.building.done || 0, a.building.total || 0), {label: t('sess.m.searchAgain'), run: () => setGrepRev(n => n + 1)});
      if (owner && !a.share_sessions) line('', html`<${Icon} name="people" size=${14} />`, f('sess.m.shared', a.name, name(owner)));
      if (a.share_sessions === 'runs') line('', html`<${Icon} name="eye" size=${14} />`, owner ? f('sess.m.sharedRuns', a.name, name(owner)) : f('sess.shareRuns', a.name));
      if (a.share_sessions === 'none') line('', html`<${Icon} name="eye" size=${14} />`, owner ? f('sess.m.sharedNone', a.name) : f('sess.shareNone', a.name));
    }
  }
  if (search && found?.fixes?.length) lines.push({key: 'fixes', tone: '', icon: html`<${Icon} name="search" size=${14} />`, text: f('sess.fixes', found.fixes.join(' '), ss.searchText(q).trim())});
  if (search && found?.too_long) lines.push({key: 'long', tone: 'warn', icon: html`<${Status} state="waiting" />`, text: f('sess.tooLong', ss.WORDS)});

  const machineCol = r => {
    const owner = ownerOf(session, answerOf(r.machine));
    return html`<span class=${cx('sv-mach', 'ell', owner && 'shared')} title=${owner ? f('sess.sharedRO', name(owner)) : undefined}>
      ${owner && html`<${Icon} name="people" size=${12} />`}${r.machine}</span>`;
  };
  const rowMenuItems = r => [
    {label: r.archived_at ? t('sess.a.archived') : t('sess.a.archive'), onClick: () => flip(r, 'archive')},
    {label: t('sess.a.edit'), onClick: () => edit(r)},
    ...(canMake(r) ? [{label: t('sess.make'), onClick: () => makeTask(r)}] : []),
    ...(ss.deletable(r, answerOf(r.machine)) ? [{label: t('sess.del'), kind: 'danger', keyName: 'Mod+Backspace', onClick: () => askTrash(r)}] : []),
  ];
  // curMore is what an open session's ⋯ holds besides the switches: on a phone making a task too.
  const curMore = r => [...(phone && canMake(r) ? [{label: t('sess.make'), onClick: () => makeTask(r)}] : []),
    ...(!inTrash && ss.deletable(r, answerOf(r.machine)) ? [{label: t('sess.del'), kind: 'danger', keyName: 'Mod+Backspace', onClick: () => askTrash(r)}] : [])];
  const [menu, setMenu] = useState(null);
  const trashActs = r => html`<span class="sv-rowact">${ss.restorable(r, answerOf(r.machine))
    && html`<${Button} kind="quiet" onClick=${e => { e.stopPropagation(); restoreRow(r); }}>${t('sess.restore')}<//>`}</span>`;
  const rowActs = r => (inTrash ? trashActs(r) : r.writable ? html`<span class="sv-rowact">
      <button type="button" class=${cx('btn', 'quiet', 'icon-only', ss.favorited(r) && 'on')} aria-label=${t('act.favorite')} title=${t('act.favorite')}
        aria-pressed=${ss.favorited(r) ? 'true' : 'false'} onClick=${e => { e.stopPropagation(); flip(r, 'favorite'); }}><${Icon} name="star" fill=${ss.favorited(r)} /></button>
      <button type="button" class="btn quiet icon-only" aria-label=${t('sess.more')} title=${t('sess.more')} aria-haspopup="menu"
        onClick=${e => { e.stopPropagation(); const b = e.currentTarget.getBoundingClientRect?.(); setMenu({key: ss.keyOf(r), at: {x: Math.max(0, (b?.right || 0) - 180), y: (b?.bottom || 0) + 4}}); }}>
        <${Icon} name="more" /></button></span>`
    : html`<span class="sv-rowact t-muted" title=${roWhy(r)}><${Icon} name="lock" size=${14} /></span>`);
  const titleCol = r => {
    const titled = html`<span class="sess-titled">${ss.favorited(r) && html`<${Star} />`}<span class="ell">${r.title || t('sess.untitled')}</span>
      ${!phone && r.archived_at && html`<${Archived} />`}${r.task && html`<${TaskLabel} task=${r.task} onOpen=${phone ? null : id => router.go({page: 'tasks', task: id})} />`}</span>`;
    if (phone) return titled;
    const {shown, more: rest} = ss.tagsShown(r.tags);
    return html`<span class="sess-cell">${titled}${(r.tags?.length || r.summary) && html`<span class="sess-line2">
      ${shown.length > 0 && html`<span class="sess-tags">${shown.map(tagChip)}${rest > 0 && html`<span class="chip">+${rest}</span>`}</span>`}
      ${r.summary && html`<span class="sess-sum ell">${r.summary}</span>`}</span>`}</span>`;
  };
  const projectCol = r => (r.project_id ? html`<span class="sess-proj ell">${projectName(r.project_id)}</span>` : html`<span class="sess-proj ell none">${phone ? t('sess.projectNone') : '—'}</span>`);
  const columns = [
    {id: 'state', label: t('sess.c.state'), width: '28px', render: r => (r.live ? html`<${Status} state=${r.live.Status === 'blocked' ? 'waiting' : 'running'} />` : ''), mobile: 'lead'},
    {id: 'title', label: t('sess.c.title'), width: 'minmax(0, 3fr)', mobile: 'primary', render: titleCol},
    ...(phone ? [
      {id: 'summary', mobile: 'line', render: r => r.summary && html`<span class="sess-sum ell">${r.summary}</span>`},
      {id: 'tags', mobile: 'line', render: r => {
        const {shown, more: rest} = ss.tagsShown(r.tags);
        return (shown.length || r.archived_at) && html`<span class="card-secondary">${shown.length > 0 && html`<span class="sess-tags">${shown.map(tagChip)}${rest > 0 && html`<span class="chip">+${rest}</span>`}</span>`}${r.archived_at && html`<${Archived} />`}</span>`;
      }},
    ] : []),
    {id: 'machine', label: t('sess.c.machine'), width: '92px', render: machineCol, mobile: 'secondary'},
    {id: 'project', label: t('sess.c.project'), width: '64px', render: projectCol, mobile: 'secondary'},
    {id: 'agent', label: t('sess.c.agent'), width: '56px', render: r => html`<span class="mono">${r.provider}</span>`, mobile: 'secondary'},
    inTrash ? {id: 'last', label: t('sess.c.deleted'), width: '104px', align: 'right', mobile: 'trailing',
      render: r => html`<span class="mono t-muted">${f('sess.deletedAt', deletedWhen(r.deleted_at || r.updated_at, at))}</span>`}
      : {id: 'last', label: t('sess.c.last'), width: '80px', align: 'right', render: r => html`<span class="mono t-muted">${when(r.last_at, at)}</span>`, mobile: 'trailing'},
    ...(phone ? [] : [{id: 'acts', label: '', width: inTrash ? '72px' : '58px', align: 'right', render: rowActs}]),
  ];
  const hitColumns = [
    {id: 'title', label: t('sess.c.title'), width: 'minmax(0, 3fr)', mobile: 'primary', render: h => html`<span class="sv-hit">
      <span class="sess-titled">${ss.favorited(h.row) && html`<${Star} />`}<span class="ell">${h.row.title || t('sess.untitled')}</span></span>
      <span class="sv-snip"><${Marked} text=${h.snippet} spans=${h.spans} /></span></span>`},
    {id: 'machine', label: t('sess.c.machine'), width: '92px', render: h => machineCol({...h.row, machine: h.machine}), mobile: 'secondary'},
    {id: 'hits', label: t('sess.c.hits'), width: '120px', align: 'right', render: h => html`<span class="mono t-muted">${f('sess.hitsAt', h.hits, when(h.at, at))}</span>`, mobile: 'trailing'},
  ];

  const writable = !!cur?.writable;
  const curOwner = cur ? ownerOf(session, answerOf(cur.machine)) : '';
  const readOnly = !cur || writable ? '' : curOwner ? f('sess.readOnly', name(curOwner), cur.machine)
    : answerOf(cur.machine)?.state === ss.answer.old ? f('sess.oldRO', cur.machine, cur.machine) : t('sess.roOther');
  const curAnswer = cur ? answerOf(cur.machine) : null;
  const trashed = cur && inTrash ? {at: cur.deleted_at || cur.updated_at, days: curAnswer?.trash_days || 0, restore: ss.restorable(cur, curAnswer) ? () => restoreRow(cur) : null} : null;
  const conv = cur && html`<${Conversation} key=${ss.keyOf(cur)} wire=${wire} row=${cur} os=${machines.find(m => m.name === cur.machine)?.os || ''} copy=${copy} toasts=${toasts} now=${at}
    onTask=${id => router.go({page: 'tasks', task: id})} project=${cur.project_id ? projectName(cur.project_id) : ''} owner=${curOwner ? name(curOwner) : ''} readOnly=${readOnly}
    trashed=${trashed} more=${curMore(cur)}
    on=${id => (id === 'edit' ? edit(cur) : flip(cur, id))} makeTask=${canMake(cur) ? () => makeTask(cur) : null} makeWhy=${canMake(cur) ? makeWhy(w, cur, online) : ''}
    at=${hitOf ? {off: hitOf.off, file: hitOf.file || ''} : null} find=${hitOf ? q : ''} />`;

  const rows = list?.rows || [];
  const totals = answers.reduce((s, a) => ({total: s.total + (a.total || 0), matched: s.matched + (a.matched || 0), running: s.running + (a.running || 0)}), {total: 0, matched: 0, running: 0});
  const hitCount = (found?.hits || []).reduce((s, h) => s + (h.hits || 0), 0);
  const summary = search ? (found && !found.loading ? f('sess.msgHead', found.hits.length, hitCount) : '')
    : list ? (inTrash ? f('sess.trashHead', totals.matched) : f('sess.counts', totals.total, totals.matched, totals.running)) : '';

  const pick = (kinds, add) => setQ(ss.replaced(q, tokens, kinds, add));
  const countOf = (facet, k) => (list?.facets?.[facet]?.[k] !== undefined ? String(list.facets[facet][k]) : undefined);
  const machineOptions = [{value: '', label: t('sess.f.any'), note: undefined},
    ...readable.map(m => {
      const owner = ownerOf(session, m);
      const off = m.state !== 'connected';
      return {value: m.name, label: m.name, sub: owner ? f('sess.m.sharedSub', name(owner)) : '', state: off ? 'offline' : 'online', note: countOf('machines', m.name), disabled: off};
    })];
  const projectIDs = Object.keys(list?.facets?.projects || {}).filter(Boolean);
  const projectOptions = [{value: '', label: t('sess.f.any')},
    ...projectIDs.map(id => ({value: id, label: projectName(id), note: countOf('projects', id)})).sort((a, b) => a.label.localeCompare(b.label)),
    {value: ss.NONE, label: t('sess.projectNone'), note: countOf('projects', '')}];
  const tagOptions = Object.entries(list?.facets?.tags || {}).sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([x, n]) => ({value: x, label: '#' + x, note: String(n)}));
  const providerOptions = [{value: '', label: t('sess.f.any')}, ...Object.entries(list?.facets?.providers || {}).sort().map(([p, n]) => ({value: p, label: p, note: String(n)}))];
  const timeOptions = [{value: '', label: t('sess.f.anyTime')}, ...ss.times(at).map(x => ({value: x.token, label: t('sess.t.' + x.id)}))];
  const sortOptions = ss.sorts.map(s => ({value: s, label: t('sess.sort.' + s)}));
  const host = c.host?.value || '', project = c.project?.value || '', provider = c.provider?.value || '';
  const time = c.last?.text || '', state = ss.stateOf(tokens);
  const chipOf = (key, label, value, set, extra = '') => ({open, toggle, ref}) => html`<button type="button" ref=${ref} class=${cx('sv-chip', set && 'set')}
    aria-haspopup="listbox" aria-expanded=${open ? 'true' : 'false'} onClick=${toggle}><span class="k">${label}</span>${value}<${Icon} name="down" />${extra && html`<${Kbd} k=${extra} />`}</button>`;
  const machineChip = html`<${Picker} label=${t('sess.f.machine')} options=${machineOptions} value=${host} want=${want.machine} onChange=${v => pick([ss.kind.host], v ? ['host:' + v] : [])}
    trigger=${chipOf('machine', t('sess.f.machine'), host || f('sess.f.machineAll', readable.length), !!host, 'm')} />`;
  const projectChip = html`<${Picker} label=${t('sess.f.project')} options=${projectOptions} value=${project} onChange=${v => pick([ss.kind.project], v ? ['project:' + v] : [])}
    trigger=${chipOf('project', t('sess.f.project'), project ? (project === ss.NONE ? t('sess.projectNone') : projectName(project)) : t('sess.f.any'), !!project)} />`;
  const tagChipPick = html`<${Picker} label=${t('sess.f.tag')} multi options=${tagOptions} value=${c.tags} want=${want.tags} onChange=${v => pick([ss.kind.tag], v.map(x => '#' + x))}
    trigger=${chipOf('tags', t('sess.f.tag'), c.tags.length ? c.tags.map(x => '#' + x).join(' ') : t('sess.f.any'), c.tags.length > 0, 't')} />`;
  const providerChip = html`<${Picker} label=${t('sess.f.source')} options=${providerOptions} value=${provider} onChange=${v => pick([ss.kind.provider], v ? ['provider:' + v] : [])}
    trigger=${chipOf('provider', t('sess.f.source'), provider || t('sess.f.any'), !!provider)} />`;
  const timeChip = html`<${Picker} label=${t('sess.f.time')} options=${timeOptions} value=${time} onChange=${v => pick(ss.timeKinds, v ? [v] : [])}
    trigger=${chipOf('time', t('sess.f.time'), timeOptions.find(o => o.value === time)?.label || time || t('sess.f.anyTime'), !!time)} />`;
  const sortChip = html`<${Picker} label=${t('sess.f.sort')} options=${sortOptions} value=${sort || ss.sorts[0]} onChange=${v => go({sort: v === ss.sorts[0] ? '' : v})}
    trigger=${chipOf('sort', t('sess.f.sort'), t('sess.sort.' + (sort || ss.sorts[0])), !!sort)} />`;
  const stateChip = html`<${Picker} label=${t('sess.f.state')} options=${ss.states.map(s => ({value: s, label: t('sess.st.' + s)}))} value=${state}
    onChange=${v => pick([ss.kind.status], ss.stateToken(v))} trigger=${chipOf('state', t('sess.f.state'), t('sess.st.' + state), state !== 'open', 's')} />`;
  const favChip = html`<button type="button" class=${cx('sv-chip', fav && 'set')} aria-pressed=${fav ? 'true' : 'false'} onClick=${() => go({fav: !fav})}>${t('sess.favOnly')}</button>`;
  const onChipsKey = e => {
    const list = focusables(chips.current);
    const j = ['ArrowLeft', 'ArrowRight'].includes(e.key) ? arrowStep(e.key, list.indexOf(chips.current.ownerDocument.activeElement), list.length) : null;
    if (j !== null && list.length) { e.preventDefault(); list[j].focus(); }
  };
  const chipRow = phone
    ? html`<div class="sv-scroll-x" ref=${chips} onKeyDown=${onChipsKey}>${machineChip}${stateChip}${favChip}
        <button type="button" class=${cx('sv-chip', (project || provider || time || c.tags.length || sort) && 'set')} onClick=${() => setFiltersOpen(true)}><${Icon} name="filter" />${t('sess.moreFilters')}</button></div>`
    : html`<div class="sv-chips" ref=${chips} onKeyDown=${onChipsKey}>${machineChip}${projectChip}${tagChipPick}${providerChip}
        <${Segmented} label=${t('sess.f.state')} value=${state} onChange=${v => pick([ss.kind.status], ss.stateToken(v))}
          options=${ss.states.map(s => ({value: s, label: t('sess.st.' + s)}))} />
        ${timeChip}${favChip}${sortChip}</div>`;
  const unknown = c.unknown.length > 0 && html`<p class="sv-unknown">${f('sess.unknown', c.unknown.join(' '))}</p>`;

  const head = html`<div class=${cx('sess-head', phone && 'sess-head-phone')}>
    <span class="sess-head-row">
      <h1 class="tasks-title ell">${t('sess.pageTitle')}</h1>
      ${summary && html`<span class="t-muted">${summary}</span>`}
      <span class="sess-head-acts"><${Button} onClick=${() => (search ? setGrepRev(n => n + 1) : load({keep: true, fresh: true}))}>${t('sess.refresh')}<//></span>
    </span>
    <div class="sv-tools">
      <div class="sv-qrow"><div class="sv-q">
        <input class="in" type="search" ref=${input} value=${text} placeholder=${t('sess.queryHint')} aria-label=${t('sess.filter')} autocomplete="off" spellcheck="false"
          onInput=${e => onType(e.currentTarget.value)} onKeyDown=${onQueryKey} />
        <button type="button" class="sv-help" aria-label=${t('sess.syntax')} title=${t('sess.syntax')} aria-expanded=${syntaxOpen ? 'true' : 'false'} onClick=${() => setSyntaxOpen(!syntaxOpen)}><${Icon} name="help" /></button>
        ${syntaxOpen && html`<${Syntax} onClose=${() => setSyntaxOpen(false)} />`}
      </div>${phone && html`<${Button} kind="quiet" on=${search} onClick=${toSearch}>${t('sess.msgSearch')}<//>`}</div>
      ${unknown}
      ${!search && chipRow}
    </div>
    <${MachineLines} lines=${lines} />
  </div>`;

  const empty = (title, sub, acts) => html`<div class="sv-empty"><p><b>${title}</b></p>${sub && html`<p class="t-muted">${sub}</p>`}${acts}</div>`;
  const dropChips = html`<span class="sv-chips">${tokens.map(x => html`<button type="button" class="sv-chip set" key=${x.text} aria-label=${f('sess.dropToken', x.text)}
    onClick=${() => setQ(ss.dropped(q, x))}>${x.text}<span class="x"><${Icon} name="close" /></span></button>`)}</span>`;
  const listBody = () => {
    if (!list) return html`<p class="empty">${t('sess.loading')}</p>`;
    if (list.error && !rows.length) return html`<p class="sess-note t-failed">${apiText(w, list.error)}</p>`;
    if (!rows.length && !list.loading) {
      if (!readable.length && !answers.length) return empty(t('sess.e.noMachine'), t('sess.e.noMachineSub'), html`<${Button} onClick=${() => router.go({page: 'machines'})}>${t('sess.e.addMachine')}<//>`);
      if (inTrash) {
        const days = [...new Set(answers.filter(a => a.trash).map(a => a.trash_days || 0))];
        return empty(t('sess.e.trash'), days.length === 1 && days[0] ? f('sess.e.trashSub', days[0]) : t('sess.e.trashKeep'),
          html`<${Button} onClick=${() => pick([ss.kind.status], [])}>${t('sess.e.open')}<//>`);
      }
      if (c.status?.value === 'agent') return empty(t('sess.e.agent'), t('sess.e.agentSub'), html`<${Button} onClick=${() => pick([ss.kind.status], [])}>${t('sess.e.open')}<//>`);
      if (fav && !tokens.length) return empty(t('sess.e.noFav'), t('sess.e.noFavSub'), html`<${Button} onClick=${() => go({fav: false})}>${t('sess.e.allSessions')}<//>`);
      return empty(t('sess.e.none'), tokens.length ? t('sess.e.noneSub') : '', html`${tokens.length > 0 && dropChips}
        ${(tokens.length > 0 || fav) && html`<${Button} onClick=${() => go({q: '', fav: false})}>${t('sess.clear')}<//>`}`);
    }
    return html`<${Table} label=${t('sess.pageTitle')} columns=${columns} rows=${rows} rowKey=${ss.keyOf} selected=${selected}
      rowClass=${r => cx((r.archived_at || !ss.fits(r, tokens, all)) && 'sess-archived', pending.has(ss.keyOf(r)) && 'sv-pending')} rowHeight=${phone ? ROW.phone : ROW.desktop}
      onSelect=${phone ? null : k => openRow(k, false)} onOpen=${k => openRow(k, phone)} active=${!(phone && cur) && !modal} onEnd=${nextPage} />`;
  };
  const hitsBody = () => {
    if (!ss.searchText(q).trim()) return empty(t('sess.msgSearch'), t('sess.msgIdle'));
    if (!found || found.loading && !found.hits.length) return html`<p class="empty">${t('sess.loading')}</p>`;
    if (found.error) return html`<p class="sess-note t-failed">${apiText(w, found.error)}</p>`;
    if (!found.hits.length) return empty(t('sess.noHits'), f('sess.noHitsSub', ss.searchText(q)));
    return html`<${Table} label=${t('sess.msgSearch')} columns=${hitColumns} rows=${found.hits} rowKey=${keyOfHit} selected=${selected}
      rowHeight=${phone ? ROW.phone : ROW.desktop} onSelect=${phone ? null : k => openRow(k, false)} onOpen=${k => openRow(k, phone)} active=${!(phone && cur) && !modal} />`;
  };
  const body = search ? hitsBody() : listBody();
  const dialogs = html`
    ${modal?.kind === 'edit' && html`<${EditSession} row=${modal.base} stale=${modal.stale} common=${Object.keys(list?.facets?.tags || {}).slice(0, 8)}
      onSave=${saveEdit} onClose=${() => setModal(null)} />`}
    ${modal?.kind === 'trash' && html`<${DeleteSession} row=${modal.base} days=${modal.days} onDelete=${sendTrash} onClose=${() => setModal(null)} />`}
    ${modal?.kind === 'make' && html`<${MakeTask} row=${modal.base} projects=${makeProjects} why=${makeWhy(w, modal.base, online)} onMake=${sendMake} onClose=${() => setModal(null)} />`}
    ${menu && (() => { const r = rowNow(menu.key); return r && html`<${RowMenu} at=${menu.at} items=${rowMenuItems(r)} onClose=${() => setMenu(null)} />`; })()}
    ${filtersOpen && html`<${Modal} title=${t('sess.filtersPage')} full onClose=${() => setFiltersOpen(false)} actions=${[
      {label: f('sess.showN', totals.matched), kind: 'primary', onClick: () => setFiltersOpen(false)},
      {label: t('sess.clear'), onClick: () => { go({q: '', fav: false, sort: ''}); setFiltersOpen(false); }},
    ]}><div class="sv-filters">
      <section><h3>${t('sess.f.project')}</h3>${projectOptions.map(o => html`<button type="button" class=${cx('sv-opt', o.value === project && 'on')} key=${o.value}
        onClick=${() => pick([ss.kind.project], o.value ? ['project:' + o.value] : [])}><span>${o.label}</span><span class="mono t-muted">${o.note ?? ''}</span></button>`)}</section>
      ${tagOptions.length > 0 && html`<section><h3>${t('sess.f.tag')}</h3><div class="sv-tagsugg">${tagOptions.map(o => html`<${Chip} key=${o.value} label=${o.label} count=${o.note}
        on=${c.tags.includes(o.value)} onClick=${() => pick([ss.kind.tag], (c.tags.includes(o.value) ? c.tags.filter(x => x !== o.value) : [...c.tags, o.value]).map(x => '#' + x))} />`)}</div></section>`}
      <section><h3>${t('sess.f.source')}</h3><${Segmented} label=${t('sess.f.source')} value=${provider} onChange=${v => pick([ss.kind.provider], v ? ['provider:' + v] : [])}
        options=${providerOptions.map(o => ({value: o.value, label: o.label}))} /></section>
      <section><h3>${t('sess.f.time')}</h3><div class="sv-tagsugg">${timeOptions.map(o => html`<${Chip} key=${o.value} label=${o.label} on=${o.value === time}
        onClick=${() => pick(ss.timeKinds, o.value ? [o.value] : [])} />`)}</div></section>
      <section><h3>${t('sess.f.sort')}</h3><${Segmented} label=${t('sess.f.sort')} value=${sort || ss.sorts[0]} onChange=${v => go({sort: v === ss.sorts[0] ? '' : v})}
        options=${sortOptions} /></section>
    </div><//>`}`;

  if (phone) {
    return html`<div class="sess sess-phone">${head}${body}
      ${open && (list || found) && html`<${Drawer} title=${cur?.title || t('sess.untitled')} onClose=${() => router.back(here({open: ''}))}
        extra=${cur && !readOnly && curMore(cur).length > 0 && html`<${Menu} bare icon="more" label=${t('sess.more')} items=${curMore(cur)} />`}>
        ${conv || html`<p class="empty">${t('sess.gone')}</p>`}<//>`}
      ${dialogs}
    </div>`;
  }
  return html`<div class="sess">
    ${head}
    <div class="sess-body">
      <div class="sess-list">${body}</div>
      <div class="sess-pane">${conv || html`<p class="empty">${open && (list || found) && !list?.loading ? t('sess.gone') : t('sess.pick')}</p>`}</div>
    </div>
    ${dialogs}
  </div>`;
}

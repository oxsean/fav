// dom is the least of a document Preact renders into, for the interaction tests: elements with attributes, styles,
// listeners that bubble, focus, and text. install() makes it the global document.
const HTML = 'http://www.w3.org/1999/xhtml';

class Node {
  constructor(doc) {
    this.ownerDocument = doc;
    this.parentNode = null;
    this.childNodes = [];
  }
  get firstChild() { return this.childNodes[0] || null; }
  get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
  get nextSibling() {
    const p = this.parentNode;
    return p ? p.childNodes[p.childNodes.indexOf(this) + 1] || null : null;
  }
  insertBefore(node, ref) {
    node.parentNode?.removeChild(node);
    const i = ref ? this.childNodes.indexOf(ref) : -1;
    this.childNodes.splice(i < 0 ? this.childNodes.length : i, 0, node);
    node.parentNode = this;
    return node;
  }
  appendChild(node) { return this.insertBefore(node, null); }
  removeChild(node) {
    const i = this.childNodes.indexOf(node);
    if (i >= 0) this.childNodes.splice(i, 1);
    node.parentNode = null;
    return node;
  }
  remove() { this.parentNode?.removeChild(this); }
  contains(n) {
    for (; n; n = n.parentNode) if (n === this) return true;
    return false;
  }
  get textContent() { return this.childNodes.map(c => c.textContent).join(''); }
}

class Text extends Node {
  constructor(doc, data) { super(doc); this.nodeType = 3; this.data = String(data); }
  get textContent() { return this.data; }
}

const events = ['click', 'dblclick', 'keydown', 'keyup', 'input', 'change', 'scroll', 'focus', 'blur', 'submit', 'pointerdown', 'mousedown',
  'dragstart', 'dragover', 'dragleave', 'drop', 'dragend'];

class Element extends Node {
  constructor(doc, ns, tag) {
    super(doc);
    this.nodeType = 1;
    this.namespaceURI = ns;
    this.localName = tag;
    this.tagName = tag.toUpperCase();
    this.attributes = new Map();
    this.style = {cssText: '', setProperty(k, v) { this[k] = v; }};
    this.listeners = {};
    this.scrollTop = 0;
    this.clientHeight = 0;
  }
  setAttribute(k, v) { this.attributes.set(k, String(v)); }
  getAttribute(k) { return this.attributes.has(k) ? this.attributes.get(k) : null; }
  hasAttribute(k) { return this.attributes.has(k); }
  removeAttribute(k) { this.attributes.delete(k); }
  get disabled() { return this.attributes.has('disabled'); }
  get className() { return this.getAttribute('class') || ''; }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter(f => f !== fn); }
  focus() {
    const doc = this.ownerDocument;
    doc.activeElement = this;
  }
  blur() { if (this.ownerDocument.activeElement === this) this.ownerDocument.activeElement = null; }
  // dispatch sends an event up from this element; it returns the event, so a test can see preventDefault.
  dispatch(type, fields = {}) {
    const e = {type, target: this, defaultPrevented: false, propagation: true, ...fields,
      preventDefault() { this.defaultPrevented = true; }, stopPropagation() { this.propagation = false; }};
    for (let n = this; n && e.propagation; n = n.parentNode) {
      e.currentTarget = n;
      for (const fn of n.listeners?.[type] || []) fn.call(n, e);
    }
    return e;
  }
  get children() { return this.childNodes.filter(c => c.nodeType === 1); }
  // all is every element under this one that match accepts, in document order.
  all(match = () => true) {
    const out = [];
    const walk = n => { for (const c of n.children) { if (match(c)) out.push(c); walk(c); } };
    walk(this);
    return out;
  }
  // find is the elements under this one with a class (".x"), a tag ("button"), or an attribute value ("[role=grid]").
  find(sel) {
    const m = /^\[([\w-]+)(?:=([^\]]*))?\]$/.exec(sel);
    if (m) return this.all(e => e.hasAttribute(m[1]) && (m[2] === undefined || e.getAttribute(m[1]) === m[2]));
    if (sel.startsWith('.')) return this.all(e => e.className.split(' ').includes(sel.slice(1)));
    return this.all(e => e.localName === sel);
  }
  one(sel) {
    const got = this.find(sel);
    if (got.length !== 1) throw new Error(`${sel}: ${got.length} elements`);
    return got[0];
  }
}
for (const t of events) Element.prototype['on' + t] = null;

export function createDocument() {
  const doc = {activeElement: null};
  doc.createElementNS = (ns, tag) => new Element(doc, ns, tag);
  doc.createElement = tag => new Element(doc, HTML, tag);
  doc.createTextNode = data => new Text(doc, data);
  doc.documentElement = doc.createElement('html');
  doc.body = doc.createElement('body');
  doc.documentElement.appendChild(doc.body);
  return doc;
}

// install makes a fresh document the global one and returns a container in its body.
export function install() {
  const doc = createDocument();
  globalThis.document = doc;
  const root = doc.createElement('div');
  doc.body.appendChild(root);
  return root;
}

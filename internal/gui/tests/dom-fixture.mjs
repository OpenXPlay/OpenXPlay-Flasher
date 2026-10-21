// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import assert from "node:assert/strict";

export class NodeFake {
  constructor(tag = "", text = "") {
    this.tag = tag; this.children = []; this.attributes = {}; this.listeners = {};
    this.className = ""; this.id = ""; this.dataset = {}; this.hidden = false; this.disabled = false; this.open = false; this.text = text;
    this.classList = { toggle: (name, enabled) => {
      const classes = new Set(this.className.split(" ").filter(Boolean));
      if (enabled) classes.add(name); else classes.delete(name);
      this.className = [...classes].join(" ");
    } };
  }
  setAttribute(key, value) {
    this.attributes[key] = value;
    if (["disabled", "checked", "open", "hidden"].includes(key)) this[key] = true;
    else this[key] = value;
    if (key.startsWith("data-")) this.dataset[key.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase())] = value;
  }
  removeAttribute(key) { delete this.attributes[key]; }
  addEventListener(event, fn) { (this.listeners[event] ||= []).push(fn); }
  append(node) { if (!(node instanceof NodeFake)) node = new NodeFake("", String(node)); node.parent = this; this.children.push(node); }
  replaceChildren(...nodes) { this.children = []; for (const node of nodes) this.append(node); }
  contains(node) { return this === node || this.children.some((c) => c.contains(node)); }
  get textContent() { return this.text + this.children.map((c) => c.textContent).join(""); }
  set textContent(text) { this.text = text; this.children = []; }
  focus() { document.activeElement = this; }
  scrollIntoView() {}
  setSelectionRange() {}
  async click() {
    assert.equal(this.disabled, false, "clicked a disabled control: " + this.textContent);
    for (const fn of this.listeners.click || []) await fn({ target: this });
  }
}
export const walk = (node) => [node, ...node.children.flatMap(walk)];


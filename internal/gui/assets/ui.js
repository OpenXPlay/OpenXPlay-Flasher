// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

// Helpers shared by the page's views: the JSON API and DOM building.

// api calls /api/<path>: GET without a body, POST with one. A failed call throws an Error whose
// code is the API's error code ("no-device", "invalid", ...), for translation.
export async function api(path, body, method) {
  const res = await fetch("/api/" + path, {
    method: method || (body === undefined ? "GET" : "POST"),
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (res.status === 204) return null;
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const err = new Error((data && data.error) || res.statusText);
    err.code = (data && data.code) || "http";
    throw err;
  }
  return data;
}

// el builds an element; attrs set attributes ("class" sets className, functions become listeners
// for the attribute name without its "on" prefix), children are nodes or text.
export function el(tag, attrs, children) {
  const node = document.createElement(tag);
  for (const [key, value] of Object.entries(attrs || {})) {
    if (typeof value === "function") node.addEventListener(key.replace(/^on/, ""), value);
    else if (key === "class") node.className = value;
    else if (value === true) node.setAttribute(key, "");
    else if (value !== false && value != null) node.setAttribute(key, value);
  }
  for (const child of [].concat(children == null ? [] : children)) {
    if (child === "" || child == null || child === false) continue;
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}

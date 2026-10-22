// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { t, lang } from "./i18n.js";
import { api, el } from "./ui.js";
import { problemText } from "./installlogic.js";
import { deviceSummary } from "./devices.js";

// Secrets live only in this module's memory and password-input properties. Never use web
// storage, value attributes, diagnostics or console output for authentication material.
const form = { profile: "", host: "", port: 22, user: "", password: "", keyFile: "", passphrase: "",
  recent: [], knownHosts: [], connected: null, error: null, busy: false, passphraseNeeded: false };
let changed = () => {}, receive = () => {}, boot = {};
export function selectedProfile() { return form.profile; }
export async function initConnection(prefs, onChanged, onReceive) {
  boot = prefs; changed = onChanged; receive = onReceive;
  try {
    const state = await api("connection");
    form.profile = state.connection?.profile || ""; form.connected = state.connection?.device || null;
    form.recent = (state.recent || []).filter((r) => r.mode === "network");
    form.knownHosts = state.knownHosts || [];
    const recent = form.recent[0];
    if (recent) { form.host=recent.host; form.port=recent.port; form.user=recent.user; }
  } catch (_) { /* The device chooser remains available if the initial read fails. */ }
  changed();
}
function clearSecrets() { form.password = ""; form.passphrase = ""; }
export async function selectProfile(id) {
  if (form.busy) return;
  form.busy = true; clearSecrets(); form.passphraseNeeded=false; form.error = null; changed();
  try { const state = await api("devices/select", { profile: id }); form.profile = id; form.connected = null; receive(state); }
  catch (error) { form.error = error; }
  form.busy = false; changed();
}
function useRecent(r) { form.host=r.host; form.port=r.port; form.user=r.user; clearSecrets(); form.error=null; changed(); }
function hostAddress() {
  const host = form.host.trim();
  const port = Number(form.port) || 22;
  return port === 22 ? host : "[" + host + "]:" + port;
}
function updateForget() {
  const button = document.getElementById("ssh-forget");
  if (button) button.hidden = !form.knownHosts.includes(hostAddress());
}
async function readKnownHosts() {
  try { form.knownHosts = (await api("connection")).knownHosts || []; }
  catch (_) { /* Leave the last confirmed host-key metadata intact if the read fails. */ }
}
export function hostKeyCommand(type) {
  const file = { "ssh-ed25519": "ed25519", "ssh-rsa": "rsa", "ecdsa-sha2-nistp256": "ecdsa", "ecdsa-sha2-nistp384": "ecdsa", "ecdsa-sha2-nistp521": "ecdsa" }[type];
  return file ? "ssh-keygen -lf /etc/ssh/ssh_host_" + file + "_key.pub" : "";
}
export async function connectSSH(acceptHostKey = "") {
  if (form.busy) return;
  form.busy = true; form.error = null; changed();
  try {
    const state = await api("ssh/connect", { host:form.host,
      port:Number(form.port),user:form.user,password:form.password,keyFile:form.keyFile,
      passphrase:form.passphrase,acceptHostKey });
    clearSecrets();form.passphraseNeeded=false;form.connected=state.target.device;receive(state);
  } catch (error) { form.error=error;form.passphraseNeeded=error.code==="ssh-key-passphrase"; }
  await readKnownHosts();
  form.busy = false; changed();
}
function input(field,label,type="text",extra={}) {
  const node=el("input",{id:"ssh-"+field,type,disabled:form.busy,autocomplete:type==="password"?"off":"on",spellcheck:"false",...extra,
    oninput:(e)=>{form[field]=e.target.value;if(field==="host"||field==="port")updateForget();}});
  node.value=form[field];
  return el("label",{},[el("span",{},label),node]);
}
function errorView() {
  const error=form.error;if(!error)return "";
  if(error.code==="ssh-host-unknown") return el("section",{class:"host-key warning",role:"alertdialog","aria-label":t("Trust this SSH host?")},[
    el("h3",{},t("Trust this SSH host?")),el("p",{},t("Check this fingerprint with the box before accepting it.")),
    el("p",{},error.host),el("p",{class:"host-key-fingerprint"},[el("span",{},error.keyType || ""), " · ", el("code",{},error.fingerprint)]),
    hostKeyCommand(error.keyType) ? el("p",{},t("Run this command on the box and compare its fingerprint:")) : "",
    hostKeyCommand(error.keyType) ? el("code",{class:"host-key-command"},hostKeyCommand(error.keyType)) : "",
    el("div",{class:"actions"},[el("button",{id:"ssh-accept",type:"button",disabled:form.busy,onclick:()=>connectSSH(error.fingerprint)},t("Accept host key and connect")),
      el("button",{type:"button",disabled:form.busy,onclick:()=>{form.error=null;clearSecrets();changed();}},t("Cancel"))])]);
  return el("p",{class:"error-box",role:"alert"},problemText(t,error));
}
export function renderConnection({root,chooser,profiles,running=false}) {
  if(profiles.length===1&&!form.profile)form.profile=profiles[0].id;
  const selected=profiles.find((p)=>p.id===form.profile);
  chooser.replaceChildren(...(profiles.length>1?[el("h3",{},t("Choose your device type")),el("div",{class:"device-types",role:"group","aria-label":t("Device type")},
    profiles.map((p)=>el("button",{id:"device-type-"+p.id,type:"button",disabled:running||form.busy,"aria-pressed":String(p.id===form.profile),onclick:()=>selectProfile(p.id)},p.id==="ufi003"?t("UFI003 USB dongle"):(p.name?.[lang()]||p.name?.en||p.id))))]:[]));
  if (!selected && profiles.length>1) {root.replaceChildren(el("p",{class:"note"},t("Choose a device type to begin.")));return {handled:true,ready:false};}
  if(selected?.transport!=="ssh")return {handled:false,ready:true};
  const connected=form.connected;
  const parts=[];
  if(connected){
    parts.push(el("p",{class:"check ok"},t("Phicomm N1 confirmed")),
      el("strong",{class:"device-identity"},deviceSummary(t, selected.name?.[lang()] || selected.name?.en, connected, connected.serialno)),
      el("p",{},t("Board serial (eth0 MAC): {serial}", {serial:connected.serialno})),
      el("button",{type:"button",disabled:running||form.busy,onclick:async()=>{try{await api("ssh/disconnect",{});form.connected=null;receive(await api("install"));}catch(error){form.error=error;}changed();}},t("Disconnect")));
  } else {
    const recent=form.recent;
    if(recent.length)parts.push(el("div",{class:"recent-hosts"},[el("span",{},t("Recent connections")),...recent.map((r)=>el("button",{type:"button",disabled:form.busy,onclick:()=>useRecent(r)},r.user+"@"+r.host+":"+r.port))]));
    const fields=[input("host",t("IP address or host name")),input("port",t("Port"),"number",{min:1,max:65535})];
    fields.push(input("user",t("User name")),input("password",t("Password"),"password"));
    if(boot.web)fields.push(input("keyFile",t("Private key file (optional)")));
    else {
      fields.push(el("button",{id:"ssh-key-pick",type:"button",disabled:form.busy,onclick:async()=>{try{const picked=await api("ssh/key/pick",{});if(picked.path){form.keyFile=picked.path;form.passphrase="";form.passphraseNeeded=false;form.error=null;}}catch(error){form.error=error;}changed();}},t("Choose private key file…")));
      if(form.keyFile)fields.push(el("div",{class:"selected-key"},[el("span",{},form.keyFile.split(/[\\/]/).at(-1)),
        el("button",{id:"ssh-key-remove",type:"button",disabled:form.busy,onclick:()=>{form.keyFile="";form.passphrase="";form.passphraseNeeded=false;form.error=null;changed();}},t("Remove"))]));
    }
    if(form.passphraseNeeded)fields.push(input("passphrase",t("Private key passphrase"),"password"));
    fields.push(el("p",{class:"note"},t("The password is used for SSH and sudo and stays in memory only.")),
      errorView(),el("div",{class:"actions"},[
        el("button",{id:"ssh-connect",type:"submit",class:"primary",disabled:form.busy},form.busy?t("Connecting…"):t("Connect")),
        el("button",{id:"ssh-forget",type:"button",hidden:!form.knownHosts.includes(hostAddress()),disabled:form.busy,onclick:async()=>{try{await api("ssh/forget",{host:form.host,port:Number(form.port)});form.error=null;await readKnownHosts();}catch(error){form.error=error;}changed();}},t("Forget this host"))]));
    // Avoid rebuilding while typing: validity is enforced by Go and submit, with native
    // required fields providing feedback without storing any input outside this module.
    const node=el("form",{class:"ssh-form",onsubmit:(event)=>{event.preventDefault();connectSSH();}},fields);
    const user=node.querySelector?.("#ssh-user");if(user)user.required=true;
    // The Connect button must stay reachable as values change without rerendering secrets.
    const button=node.querySelector?.("#ssh-connect");if(button)button.disabled=form.busy;
    parts.push(node);
  }
  parts.push(el("p",{class:"note"},t("Install Armbian yourself before connecting. The Flasher installs device support and the relay.")),
    el("a",{href:boot.links?.[lang()]?.phicommN1,"data-open":""},t("Phicomm N1 setup guide")));
  const focused=root.contains(document.activeElement)?document.activeElement.id:"";
  const active=document.activeElement;
  const selection=focused&&typeof active.selectionStart==="number"?[active.selectionStart,active.selectionEnd]:null;
  root.replaceChildren(...parts);
  const again=focused?document.getElementById(focused):null;if(again&&!again.disabled){again.focus();if(selection)again.setSelectionRange(...selection);}
  return {handled:true,ready:!!connected};
}

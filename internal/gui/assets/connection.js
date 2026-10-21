// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { t, lang } from "./i18n.js";
import { api, el } from "./ui.js";
import { problemText } from "./installlogic.js";

// Secrets live only in this module's memory and password-input properties. Never use web
// storage, value attributes, diagnostics or console output for authentication material.
const form = { profile: "", mode: "usb", host: "", port: 22, user: "", password: "", keyFile: "", passphrase: "",
  recent: [], connected: null, error: null, busy: false, passphraseNeeded: false };
let changed = () => {}, receive = () => {}, boot = {};
export function selectedProfile() { return form.profile; }
export async function initConnection(prefs, onChanged, onReceive) {
  boot = prefs; changed = onChanged; receive = onReceive;
  try {
    const state = await api("connection");
    form.profile = state.connection?.profile || ""; form.connected = state.connection?.device || null; form.recent = state.recent || [];
    if (form.connected) form.mode = form.connected.connection;
    else {const recent=form.recent.find((r)=>r.mode===form.mode);if(recent){form.host=recent.host;form.port=recent.port;form.user=recent.user;}}
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
function switchMode(mode) {
  form.mode=mode;form.connected=null;clearSecrets();form.error=null;
  const recent=form.recent.find((r)=>r.mode===mode);if(recent)useRecent(recent);else changed();
}
export async function connectSSH(acceptHostKey = "") {
  if (form.busy) return;
  form.busy = true; form.error = null; changed();
  try {
    const state = await api("ssh/connect", { mode:form.mode,host:form.mode==="usb"?"192.168.201.1":form.host,
      port:form.mode==="usb"?22:Number(form.port),user:form.user,password:form.password,keyFile:form.keyFile,
      passphrase:form.passphrase,acceptHostKey });
    clearSecrets();form.passphraseNeeded=false;form.connected=state.target.device;receive(state);
  } catch (error) { form.error=error;form.passphraseNeeded=error.code==="ssh-key-passphrase"; }
  form.busy = false; changed();
}
function input(field,label,type="text",extra={}) {
  const node=el("input",{id:"ssh-"+field,type,disabled:form.busy,autocomplete:type==="password"?"off":"on",spellcheck:"false",...extra,
    oninput:(e)=>{form[field]=e.target.value;}});
  node.value=form[field];
  return el("label",{},[el("span",{},label),node]);
}
function errorView() {
  const error=form.error;if(!error)return "";
  if(error.code==="ssh-host-unknown") return el("section",{class:"host-key warning",role:"alertdialog","aria-label":t("Trust this SSH host?")},[
    el("h3",{},t("Trust this SSH host?")),el("p",{},t("Check this fingerprint with the box before accepting it.")),
    el("p",{},error.host),el("code",{},error.fingerprint),
    el("div",{class:"actions"},[el("button",{id:"ssh-accept",type:"button",disabled:form.busy,onclick:()=>connectSSH(error.fingerprint)},t("Accept host key and connect")),
      el("button",{type:"button",disabled:form.busy,onclick:()=>{form.error=null;clearSecrets();changed();}},t("Cancel"))])]);
  return el("p",{class:"error-box",role:"alert"},problemText(t,error));
}
export function renderConnection({root,chooser,profiles,devices,running=false}) {
  if(profiles.length===1&&!form.profile)form.profile=profiles[0].id;
  const selected=profiles.find((p)=>p.id===form.profile);
  chooser.replaceChildren(...(profiles.length>1?[el("h3",{},t("Choose your device type")),el("div",{class:"device-types",role:"group","aria-label":t("Device type")},
    profiles.map((p)=>el("button",{id:"device-type-"+p.id,type:"button",disabled:running||form.busy,"aria-pressed":String(p.id===form.profile),onclick:()=>selectProfile(p.id)},p.id==="ufi003"?t("UFI003 USB dongle"):(p.name?.[lang()]||p.name?.en||p.id))))]:[]));
  if (!selected && profiles.length>1) {root.replaceChildren(el("p",{class:"note"},t("Choose a device type to begin.")));return {handled:true,ready:false};}
  if(selected?.transport!=="ssh")return {handled:false,ready:true};
  const gadget=devices?.candidates?.find((c)=>c.mode==="ssh"&&!c.host&&c.profiles?.includes(form.profile));
  const connected=form.connected;
  const parts=[el("div",{class:"actions connection-modes",role:"group","aria-label":t("Connection")},[
    el("button",{id:"ssh-mode-usb",type:"button",disabled:running||form.busy||!!connected,"aria-pressed":String(form.mode==="usb"),onclick:()=>switchMode("usb")},t("USB")),
    el("button",{id:"ssh-mode-network",type:"button",disabled:running||form.busy||!!connected,"aria-pressed":String(form.mode==="network"),onclick:()=>switchMode("network")},t("Network (Ethernet or Wi-Fi)")),
  ])];
  if(connected){
    parts.push(el("p",{class:"check ok"},t("Phicomm N1 confirmed")),el("p",{},connected.host+":"+connected.port+" · "+connected.serialno),
      el("button",{type:"button",disabled:running||form.busy,onclick:async()=>{try{await api("ssh/disconnect",{});form.connected=null;receive(await api("install"));}catch(error){form.error=error;}changed();}},t("Disconnect")));
  } else {
    if(form.mode==="usb")parts.push(el("p",{class:gadget?"check ok":"note"},gadget?t("Phicomm N1 connected over USB"):t("Connect the N1 OTG port to this computer and keep the box powered.")),el("p",{},"192.168.201.1:22"));
    if(form.mode==="network"||gadget){
      const recent=form.recent.filter((r)=>r.mode===form.mode);
      if(recent.length)parts.push(el("div",{class:"recent-hosts"},[el("span",{},t("Recent connections")),...recent.map((r)=>el("button",{type:"button",disabled:form.busy,onclick:()=>useRecent(r)},r.user+"@"+r.host+":"+r.port))]));
      const fields=[];
      if(form.mode==="network")fields.push(input("host",t("IP address or host name")),input("port",t("Port"),"number",{min:1,max:65535}));
      fields.push(input("user",t("User name")),input("password",t("Password"),"password"),input("keyFile",t("Private key file (optional)")));
      if(!boot.web)fields.push(el("button",{type:"button",disabled:form.busy,onclick:async()=>{try{const picked=await api("ssh/key/pick",{});if(picked.path)form.keyFile=picked.path;}catch(error){form.error=error;}changed();}},t("Choose private key…")));
      if(form.passphraseNeeded)fields.push(input("passphrase",t("Private key passphrase"),"password"));
      fields.push(el("p",{class:"note"},t("The password is used for SSH and sudo and stays in memory only.")),
        errorView(),el("div",{class:"actions"},[
          el("button",{id:"ssh-connect",type:"submit",class:"primary",disabled:form.busy},form.busy?t("Connecting…"):t("Connect")),
          el("button",{id:"ssh-forget",type:"button",disabled:form.busy,onclick:async()=>{try{await api("ssh/forget",{host:form.mode==="usb"?"192.168.201.1":form.host,port:form.mode==="usb"?22:Number(form.port)});form.error=null;}catch(error){form.error=error;}changed();}},t("Forget this host"))]));
      // Avoid rebuilding while typing: validity is enforced by Go and submit, with native
      // required fields providing feedback without storing any input outside this module.
      const node=el("form",{class:"ssh-form",onsubmit:(event)=>{event.preventDefault();connectSSH();}},fields);
      const user=node.querySelector?.("#ssh-user");if(user)user.required=true;
      // The Connect button must stay reachable as values change without rerendering secrets.
      const button=node.querySelector?.("#ssh-connect");if(button)button.disabled=form.busy;
      parts.push(node);
    }
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

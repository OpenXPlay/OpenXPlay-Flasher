// SPDX-FileCopyrightText: 2026 OpenXPlay.org
// SPDX-License-Identifier: AGPL-3.0-only
// Created by riko chen on 2026-10-21

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { NodeFake, walk } from "./dom-fixture.mjs";
import { setLang, t } from "../assets/i18n.js";

const profiles=["ufi003","phicomm-n1"].map((id)=>JSON.parse(readFileSync(new URL("../../profile/profiles/"+id+".json",import.meta.url))));
for(const language of ["en","zh-CN"])test("device type, USB and network forms, host-key consent in "+language,async()=>{
 setLang(language);globalThis.Node=NodeFake;
 const root=new NodeFake("div"),chooser=new NodeFake("div"),body=new NodeFake("body");body.append(root);body.append(chooser);
 globalThis.document={activeElement:body,createElement:(tag)=>new NodeFake(tag),createTextNode:(text)=>new NodeFake("",text),getElementById:(id)=>walk(body).find((n)=>n.id===id)};
 const page=await import("../assets/connection.js?"+language);
 const calls=[];let accepted=false,result;
 const state={target:{profile:"phicomm-n1",device:{mode:"ssh",connection:"network",host:"n1.test",port:22,serialno:"021122334455"}}};
 globalThis.fetch=async(url,options)=>{
  const input=options.body?JSON.parse(options.body):null;calls.push({url,input});
  const data=url==="/api/connection"?{connection:{profile:""},recent:[]}:url==="/api/devices/select"?{}:
    url==="/api/ssh/connect"&&!input.acceptHostKey?{code:"ssh-host-unknown",host:"n1.test:22",fingerprint:"SHA256:fixture",keyType:"ssh-ed25519"}:state;
  if(url==="/api/ssh/connect"&&input.acceptHostKey)accepted=true;
  return {ok:!data.code,status:data.code?409:200,json:async()=>data};
 };
 const render=()=>page.renderConnection({root,chooser,profiles,devices:{candidates:[{mode:"ssh",profiles:["phicomm-n1"],usb:{vendorId:0x1d6b,productId:0x104}}]}});
 await page.initConnection({web:true,links:{[language]:{phicommN1:"https://openxplay.org/phicomm-n1"}}},render,(s)=>{result=s;});
 assert.equal(walk(chooser).filter((n)=>n.tag==="button").length,2);
 assert.match(root.textContent,new RegExp(t("Choose a device type to begin.").replace(/[.*+?^${}()|[\]\\]/g,"\\$&")));
 await document.getElementById("device-type-phicomm-n1").click();
 assert.ok(root.textContent.includes(t("Phicomm N1 connected over USB")));
 assert.ok(root.textContent.includes("192.168.201.1:22"));assert.equal(document.getElementById("ssh-host"),undefined);
 assert.equal(document.getElementById("ssh-password").type,"password");assert.equal(document.getElementById("ssh-password").attributes.value,undefined);
 await document.getElementById("ssh-mode-network").click();
 for(const [id,value] of [["ssh-host","n1.test"],["ssh-user","riko"],["ssh-password","node-memory-secret"]]){
  const input=document.getElementById(id);input.value=value;for(const event of input.listeners.input||[])event({target:input});
 }
 assert.ok(document.getElementById("ssh-port"));
 await page.connectSSH();
 assert.ok(root.textContent.includes(t("Trust this SSH host?")));assert.ok(root.textContent.includes("SHA256:fixture"));assert.equal(accepted,false);
 assert.ok(root.textContent.includes("ssh-ed25519"));
 assert.ok(root.textContent.includes("ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub"));
 assert.ok(document.getElementById("ssh-keyFile"),"browser mode keeps a key path field");
 await document.getElementById("ssh-accept").click();
 assert.equal(accepted,true);assert.equal(result,state);assert.equal(document.getElementById("ssh-password"),undefined);
 assert.ok(root.textContent.includes(t("Phicomm N1 confirmed")));
 assert.ok(root.textContent.includes(t("Board serial (eth0 MAC): {serial}",{serial:"021122334455"})));
 assert.ok(root.textContent.includes(t("Phicomm N1")+" · n1.test"));
 assert.equal(calls.findLast((c)=>c.url==="/api/ssh/connect").input.password,"node-memory-secret");
});

for (const language of ["en", "zh-CN"]) test("N1 without USB gadget, native key selection, stored hosts and typed errors in " + language, async () => {
  setLang(language); globalThis.Node=NodeFake;
  const root=new NodeFake("div"), chooser=new NodeFake("div"), body=new NodeFake("body"); body.append(root); body.append(chooser);
  globalThis.document={activeElement:body,createElement:(tag)=>new NodeFake(tag),createTextNode:(value)=>new NodeFake("",value),getElementById:(id)=>walk(body).find((n)=>n.id===id)};
  const page=await import("../assets/connection.js?review-"+language);
  let knownHosts=["n1.test", "[n1.test]:2222"], error="ssh-timeout", request;
  globalThis.fetch=async(url, options)=>{
    request={url, input:options.body?JSON.parse(options.body):null};
    let data={};
    if(url==="/api/connection")data={connection:{profile:"phicomm-n1"},knownHosts,recent:[]};
    if(url==="/api/ssh/key/pick")data={path:"/fake/keys/n1_ed25519"};
    if(url==="/api/ssh/forget")knownHosts=[];
    if(url==="/api/ssh/connect")data={code:error, keyType:"ssh-ed25519", host:"n1.test:22", fingerprint:"SHA256:fixture"};
    return {ok:!data.code,status:data.code?409:200,json:async()=>data};
  };
  let devices={candidates:[]};
  const render=()=>page.renderConnection({root,chooser,profiles,devices});
  await page.initConnection({web:false,links:{}},render,()=>{});
  assert.equal(document.getElementById("ssh-mode-network").attributes["aria-pressed"],"true");
  assert.ok(document.getElementById("ssh-host"));
  assert.equal(document.getElementById("ssh-keyFile"),undefined,"native mode has no key path field");
  assert.equal(document.getElementById("ssh-forget").hidden,true);
  devices={candidates:[{mode:"ssh",profiles:["phicomm-n1"]}]};render();
  assert.equal(document.getElementById("ssh-mode-usb").attributes["aria-pressed"],"true","a late initial scan updates the untouched default");
  devices={candidates:[]};render();
  assert.equal(document.getElementById("ssh-mode-network").attributes["aria-pressed"],"true");
  await document.getElementById("ssh-mode-usb").click();
  assert.equal(document.getElementById("ssh-connect"),undefined);
  assert.ok(root.textContent.includes(t("No N1 USB management device was detected. Fresh Armbian has no USB management connection: install device support over the network (Ethernet or Wi-Fi) first. USB works after device support is installed.")));
  assert.ok(root.textContent.includes(t("Use the USB port next to HDMI and a USB A-to-A cable with VBUS cut, or a VBUS-blocking adapter. The computer must be the USB host. On a Mac with USB-C, use a C-to-A adapter and an A-to-A cable; a USB-A-to-C cable will not work.")));
  await document.getElementById("ssh-switch-network").click();
  devices={candidates:[{mode:"ssh",profiles:["phicomm-n1"]}]};render();
  assert.equal(document.getElementById("ssh-mode-network").attributes["aria-pressed"],"true","a user's selection survives later detection");
  const type=(field,value)=>{const input=document.getElementById("ssh-"+field); input.value=value; input.listeners.input[0]({target:input});};
  type("host","n1.test"); assert.equal(document.getElementById("ssh-forget").hidden,false);
  type("port","2223"); assert.equal(document.getElementById("ssh-forget").hidden,true);
  type("port","2222"); assert.equal(document.getElementById("ssh-forget").hidden,false);
  type("host","new.test"); assert.equal(document.getElementById("ssh-forget").hidden,true);
  type("host","n1.test"); type("port","22");
  await document.getElementById("ssh-key-pick").click();
  assert.ok(root.textContent.includes("n1_ed25519")); assert.ok(!root.textContent.includes("/fake/keys"));
  assert.ok(document.getElementById("ssh-key-remove"));
  type("user","riko");
  await page.connectSSH();
  assert.ok(root.textContent.includes(t("SSH timed out. Check the address and that the box is powered on. If a proxy or VPN app intercepts local connections, set this address to direct.")));
  await document.getElementById("ssh-key-remove").click(); assert.ok(!root.textContent.includes("n1_ed25519"));
  for (const [code, message] of [
    ["ssh-refused","SSH connection refused: no SSH server is listening on that port. Check the port and enable SSH on the box."],
    ["ssh-no-greeting","No SSH greeting was received. Check the address and that the box is powered on. If a proxy or VPN app intercepts local connections, set this address to direct."],
    ["ssh-authentication","SSH authentication failed. Check the user name, password or private key."],
    ["ssh-sudo-incorrect-password","sudo rejected the login password. Check it and try again."],
  ]) {
    error=code; await page.connectSSH(); assert.ok(root.textContent.includes(t(message)),code);
  }
  error="ssh-host-unknown"; await page.connectSSH();
  assert.ok(root.textContent.includes("ssh-ed25519"));
  assert.ok(root.textContent.includes("ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub"));
  assert.equal(page.hostKeyCommand("ssh-rsa"),"ssh-keygen -lf /etc/ssh/ssh_host_rsa_key.pub");
  assert.equal(page.hostKeyCommand("ecdsa-sha2-nistp384"),"ssh-keygen -lf /etc/ssh/ssh_host_ecdsa_key.pub");
  assert.equal(page.hostKeyCommand("untrusted;type"),"");
  await document.getElementById("ssh-forget").click(); assert.equal(document.getElementById("ssh-forget").hidden,true);
});

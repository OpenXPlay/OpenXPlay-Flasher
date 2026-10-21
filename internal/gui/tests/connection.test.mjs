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
    url==="/api/ssh/connect"&&!input.acceptHostKey?{code:"ssh-host-unknown",host:"n1.test:22",fingerprint:"SHA256:fixture"}:state;
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
 await document.getElementById("ssh-accept").click();
 assert.equal(accepted,true);assert.equal(result,state);assert.equal(document.getElementById("ssh-password"),undefined);
 assert.ok(root.textContent.includes(t("Phicomm N1 confirmed")));
 assert.equal(calls.findLast((c)=>c.url==="/api/ssh/connect").input.password,"node-memory-secret");
});

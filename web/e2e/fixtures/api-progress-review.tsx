// Development-only interactive fixture. Never imported by the production entry.
import { createRoot } from "react-dom/client";
import { useState } from "react";
import { LocaleProvider, useLocale } from "../../src/shared/i18n";
import { Button, TooltipProvider } from "../../src/shared/ui";
import { CatalogAPIUsage } from "../../src/products/curation/research/iface/CatalogAPIUsage";
import { CurationAgentWork } from "../../src/products/curation/iface/CurationAgentWork";
import type { IntelligenceJob } from "../../src/products/curation/domain/types";
import "../../src/styles.css";
import "../../src/products/curation/iface/curation-workspace.css";

const stamp = '2026-09-11T00:00:00Z';
const definitions = [
  ['OWN_PRODUCT', 'OpenWebNinja', 'Real-Time Product Search', true],
  ['OWN_WEB', 'OpenWebNinja', 'Real-Time Web Search', false],
  ['NAVER_WEBKR', 'NAVER', 'Webkr', true],
  ['SERP_GOOGLE', 'SerpApi', 'Google Search', false],
  ['ELEVENST_HTML', '11번가', 'Public product page', true],
] as const;
const apis = definitions.map(([id, apiProvider, apiProduct, enabled], i) => ({id, apiProvider, apiProduct, quotaScope:id, configured:true,
  control:{enabled,version:1}, localRequestsPerMinute:10, localDailyLimit:100, requests24h:32+i*12, notFound24h:i, failures24h:[], estimatedRemaining:undefined}));
const amazon = {schemaVersion:'vitlane.catalog-api-usage.v3',source:'AMAZON',configured:true,control:{enabled:true,version:1,updatedAt:stamp},enabled:true,mode:'STUB',quota:{remaining:45,limit:100,observedAt:stamp,resetAt:stamp},estimatedRemaining:42,requests24h:128,succeeded24h:127,failures24h:[]};
let cancel = () => {};
const writes: unknown[] = [];
Object.assign(window, {reviewWrites:writes});
window.fetch = async (input, init) => {
  const url = new URL(String(input), location.href), path=url.pathname;
  const body=init?.body ? JSON.parse(String(init.body)):undefined;
  if(init?.method && init.method !== 'GET') writes.push({path,body});
  let data: unknown;
  if(path==='/api/v1/admin/catalog-apis') data={apis};
  else if(path.includes('/catalog-sources/amazon/')) {
    if(body) { amazon.control={...amazon.control, enabled:body.enabled,version:amazon.control.version+1}; amazon.enabled=body.enabled; }
    data=amazon;
  } else if(path.includes('/catalog-apis/')) {
    const row=apis.find(a=>path.includes(`/${a.id}/`));
    if(!row) throw new Error(`Unexpected fixture request ${path}`);
    if(body) { if(body.expectedVersion!==row.control.version) throw new Error('stale version'); row.control={enabled:body.enabled,version:row.control.version+1}; }
    data=row;
  } else if(path.endsWith('/cancel')) { cancel(); data={cancelledJobs:1}; }
  else throw new Error(`Fixture refuses non-fixture request ${path}`);
  return new Response(JSON.stringify(data),{status:200,headers:{'Content-Type':'application/json'}});
};
function Review() {
  const {setLocale}=useLocale();
  const [step,setStep]=useState(1), [cancelled,setCancelled]=useState(false);
  cancel=()=>setCancelled(true);
  const kinds=['INTERPRETING','SEARCHING_CATALOG','RANKING','SUBMITTING'] as const;
  const job: IntelligenceJob={jobId:'review',actionId:'review-action',targetId:'review-target',targetKind:'PLANNING_TASK',provider:'MANAGED',status:cancelled?'CANCELLED':'RUNNING',retryable:false,attempt:1,
    steps:kinds.slice(0,step+1).map((kind,i)=>({id:`s${i}`,kind,status:i===step?'RUNNING':'SUCCEEDED',startedAt:stamp}))};
  const progress=new URLSearchParams(location.search).get('mode')==='progress';
  return <main style={{maxWidth:1320,margin:'auto',padding:'24px'}}>
    <nav style={{display:'flex',gap:12,flexWrap:'wrap',marginBottom:32}}>
      <span>개발 검토 · 합성 데이터 · 외부 API 호출 없음</span>
      <a href="?mode=operator">API 화면</a><a href="?mode=progress">큐레이션 로딩</a>
      <Button onClick={()=>setLocale('ko-KR')}>KO</Button><Button onClick={()=>setLocale('en-US')}>EN</Button>
      <Button onClick={()=>document.documentElement.classList.toggle('dark')}>Light / Dark</Button>
    </nav>
    {progress ? <><div style={{marginBottom:32}}><Button style={{whiteSpace:"normal",maxWidth:"100%"}} onClick={()=>{setCancelled(false);setStep((step+1)%4)}}>다음 단계 / 다시 시작</Button></div><CurationAgentWork jobs={[job]} />{cancelled && <p>취소됨 · 위 버튼으로 다시 시작</p>}</> : <CatalogAPIUsage includeAmazon />}
  </main>;
}
document.documentElement.classList.add('dark');
createRoot(document.getElementById('root')!).render(<LocaleProvider><TooltipProvider><Review /></TooltipProvider></LocaleProvider>);

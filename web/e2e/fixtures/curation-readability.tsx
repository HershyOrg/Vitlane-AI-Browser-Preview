// Isolated, synthetic view of real components; excluded from production entry points.
import { ProductVerticalBadge } from "../../src/products/curation/iface/ProductVerticalBadge";
import { createRoot } from "react-dom/client";
import { LocaleProvider } from "../../src/shared/i18n";
import { AppearanceProvider, TooltipProvider } from "../../src/shared/ui";
import { BudgetProvider, BudgetTargetContext } from "../../src/products/curation/app/useBudget";
import { CriteriaEditor, TargetComparison } from "../../src/products/curation/iface/ResearchComparison";
import { CurationCandidateCard } from "../../src/products/curation/iface/CurationCandidateCard";
import { CurationFollowUpBubble } from "../../src/products/curation/iface/CurationFollowUps";
import { CurationAgentRunningPanel } from "../../src/products/curation/iface/CurationAgentWork";
import type { ResearchCriteria, AxisAssessment } from "../../src/products/curation/domain/researchCriteria";
import type { CandidatePresentation } from "../../src/products/curation/domain/candidatePresentation";
import type { LiveCatalogProduct } from "../../src/products/curation/research/infra/liveCatalogReviewApi";
import type { FollowUpMessage, IntelligenceJob } from "../../src/products/curation/domain/types";
import "../../src/styles.css";
import "../../src/product-shell.css";
import "../../src/order-operations.css";
import "../../src/catalog-surfaces.css";
import "../../src/account-surfaces.css";
import "../../src/products/curation/iface/catalog-curation-research.css";
import "../../src/products/curation/iface/curation-workspace.css";
import "../../src/products/curation/iface/budget.css";

const criteria: ResearchCriteria = { schemaVersion: "vitlane.target-criteria.v1", version: 1, subject: { label: "Pen", productType: "pen" }, exclusions: [], axes: [{axisId:"portable",label:"휴대성 / Portability",definition:"Portable",importance:5,usesPrice:false,usesVisualEvidence:false,origin:"REQUEST"}] };
const assessment: AxisAssessment = { schemaVersion:"vitlane.axis-assessment.v1",source:"ELEVENST",observationHash:"fixture",criteria,weights:[100],scores:[{axisId:"portable",scorePercent:90,basis:"UNKNOWN",explanation:"Synthetic evaluation",factIds:[]}],totalScore:90,totalBasisPoints:9000,contentLocale:"ko-KR",roundId:"fixture",modelKey:"fixture",createdAt:"2026-09-13T00:00:00Z" };
const product: LiveCatalogProduct = {candidateId:"pen",title:"Pen",currency:"KRW",priceMinimumMinor:98000,priceMaximumMinor:98000,description:"",categories:[],features:[],specifications:[],axisAssessment:assessment};
const peer: LiveCatalogProduct = {...product,candidateId:"peer",axisAssessment:{...assessment,totalScore:50,scores:[{...assessment.scores[0],scorePercent:50}]}};
const candidate = {id:"pen",title:"Pen / 만년필",source:"ELEVENST",price:{kind:"OBSERVED",amountMinor:98000,currency:"KRW"},priceScope:"PRODUCT",purchaseRoute:"EXTERNAL",features:[],specifications:[],axisAssessment:assessment} as CandidatePresentation;
const message: FollowUpMessage = {id:"proposal",responseId:"response",kind:"PROPOSAL",status:"PENDING",version:1,createdAt:assessment.createdAt,content:{code:"LOW_AXIS_FIT",body:"Synthetic pending proposal / 테스트 제의"}};
const job: IntelligenceJob = {jobId:"job",actionId:"action",targetKind:"PLANNING_TASK",targetId:"task",provider:"MANAGED",status:"RUNNING",retryable:false,attempt:1,steps:[{kind:"INTERPRETING",status:"RUNNING",startedAt:assessment.createdAt}]};
createRoot(document.getElementById("root")!).render(<LocaleProvider><AppearanceProvider><TooltipProvider>
 <main className="curation-workspace" style={{padding:16,maxWidth:760,margin:"auto"}}>
  <div className="catalog-ui-target__heading"><h2>가벼운 무선 청소기 / Lightweight cordless vacuum</h2><ProductVerticalBadge vertical="ELECTRONICS" /></div>
  <BudgetProvider curationId="readability" targets={[{id:"target",title:"Pen"}]}><BudgetTargetContext.Provider value="target">
   <CriteriaEditor criteria={criteria} curationId="readability" targetId="target" version={1} onSave={()=>{}} />
   <TargetComparison criteria={criteria} products={[product]} cohort={[product,peer]}>{()=> <div style={{width:"min(100%, 15rem)"}}><CurationCandidateCard candidate={candidate} /></div>}</TargetComparison>
  </BudgetTargetContext.Provider></BudgetProvider>
  <CurationFollowUpBubble message={message} onRespond={()=>{}} />
  <CurationFollowUpBubble message={{...message,id:"error",kind:"ERROR",content:{code:"RESEARCH_FAILED",targetTitle:"Pen"}}} onRespond={()=>{}} />
  <CurationAgentRunningPanel job={job} />
 </main>
</TooltipProvider></AppearanceProvider></LocaleProvider>);

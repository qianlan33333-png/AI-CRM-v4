import {
  listRadarClickHistory,
  getRadarClickHistory,
} from "./generated/p4-radar-click-history/p4-radar-click-history";
import {
  type RadarClickHistory,
  type MarketingConfigHistoryConfig,
  type MarketingConfigHistoryRule,
} from "./generated/health.schemas";
import {
  listMarketingConfigHistoryConfigs,
  getMarketingConfigHistoryConfig,
  listMarketingConfigHistoryRules,
  getMarketingConfigHistoryRule,
} from "./generated/p4-marketing-config-history/p4-marketing-config-history";
import { apiRequestOptions, unwrapGenerated } from './transport';

export type HistoryKind = 'radar_click' | 'marketing_config' | 'marketing_rule';
export type HistoryItem = RadarClickHistory | MarketingConfigHistoryConfig | MarketingConfigHistoryRule;
export type HistoryPage = {items:HistoryItem[];total:number;offset:number;limit:number};
type Row = Record<string, unknown>;
const invalid = (): never => {throw new Error('历史响应不符合只读契约');};
const integer = (v:unknown,min?:number):v is number => typeof v==='number' && Number.isSafeInteger(v) && (min===undefined || v>=min);
const int32 = (v:unknown):v is number => integer(v) && v>=-2147483648 && v<=2147483647;
const instant = (v:unknown):boolean => typeof v==='string' && Number.isFinite(Date.parse(v));
function object(v:unknown,keys:string[]):Row {
 if(!v || typeof v!=='object' || Array.isArray(v) || Object.keys(v).length!==keys.length || Object.keys(v).some(k=>!keys.includes(k))) invalid();
 return v as Row;
}
function safety(v:Row):void {if(v.source!=='v1_history' || v.read_only!==true || v.real_external_call_executed!==false) invalid();}
function item(kind:HistoryKind,value:unknown):HistoryItem {
 switch(kind){
case 'radar_click': {
const r=object(value,["id","source_id","link_source_id","radar_link_id","customer_id","code","raw_stage","source_channel","target_type_snapshot","source_channel_snapshot","error_code","created_at"]);
if(!(integer(r.id,1)) || !(integer(r.source_id)) || !(integer(r.link_source_id)) || !(r.radar_link_id===null || integer(r.radar_link_id,1)) || !(r.customer_id===null || integer(r.customer_id,1)) || !(typeof r.code==='string') || !(typeof r.raw_stage==='string') || !(typeof r.source_channel==='string') || !(typeof r.target_type_snapshot==='string') || !(typeof r.source_channel_snapshot==='string') || !(typeof r.error_code==='string') || !(instant(r.created_at))) invalid();
return r as unknown as RadarClickHistory;
}
case 'marketing_config': {
const r=object(value,["id","source_id","automation_key","automation_name","target_event","channel_type","original_status","do_not_start_after_hour","created_at","updated_at"]);
if(!(integer(r.id,1)) || !(integer(r.source_id)) || !(typeof r.automation_key==='string') || !(typeof r.automation_name==='string') || !(typeof r.target_event==='string') || !(typeof r.channel_type==='string') || !(typeof r.original_status==='string') || !(int32(r.do_not_start_after_hour)) || !(instant(r.created_at)) || !(instant(r.updated_at))) invalid();
return r as unknown as MarketingConfigHistoryConfig;
}
case 'marketing_rule': {
const r=object(value,["id","source_id","config_id","config_source_id","questionnaire_source_id","question_source_id","rule_code","rule_name","answer_match_type","score_delta","segment_hint","stage_hint","original_active","sort_order","created_at","updated_at"]);
if(!(integer(r.id,1)) || !(integer(r.source_id)) || !(integer(r.config_id,1)) || !(integer(r.config_source_id)) || !(r.questionnaire_source_id===null || integer(r.questionnaire_source_id,1)) || !(r.question_source_id===null || integer(r.question_source_id,1)) || !(typeof r.rule_code==='string') || !(typeof r.rule_name==='string') || !(typeof r.answer_match_type==='string') || !(int32(r.score_delta)) || !(typeof r.segment_hint==='string') || !(typeof r.stage_hint==='string') || !(typeof r.original_active==='boolean') || !(int32(r.sort_order)) || !(instant(r.created_at)) || !(instant(r.updated_at))) invalid();
return r as unknown as MarketingConfigHistoryRule;
}
default:return invalid();
}}
export async function readHistoryPage(kind:HistoryKind,offset=0,limit=20):Promise<HistoryPage> {
if(!integer(offset,0) || offset>2147483647 || !integer(limit,1) || limit>100) throw new Error('历史分页参数无效');
let value:unknown;
switch(kind){
case 'radar_click':value=unwrapGenerated(await listRadarClickHistory({offset,limit},apiRequestOptions()));break;
case 'marketing_config':value=unwrapGenerated(await listMarketingConfigHistoryConfigs({offset,limit},apiRequestOptions()));break;
case 'marketing_rule':value=unwrapGenerated(await listMarketingConfigHistoryRules({offset,limit},apiRequestOptions()));break;
default:return invalid();
}
const r=object(value,['source','read_only','real_external_call_executed','items','total','offset','limit']);safety(r);
if(!Array.isArray(r.items) || !integer(r.total,0) || r.offset!==offset || r.limit!==limit) invalid();
const values=(r.items as unknown[]).map(v=>item(kind,v));
if(values.length!==Math.min(limit,Math.max(0,(r.total as number)-offset)) || new Set(values.map(v=>v.id)).size!==values.length) invalid();
return {items:values,total:r.total as number,offset,limit};
}
export async function readHistoryDetail(kind:HistoryKind,id:number):Promise<HistoryItem>{
if(!integer(id,1)) throw new Error('历史ID无效');
let value:unknown;
switch(kind){
case 'radar_click':value=unwrapGenerated(await getRadarClickHistory(id,apiRequestOptions()));break;
case 'marketing_config':value=unwrapGenerated(await getMarketingConfigHistoryConfig(id,apiRequestOptions()));break;
case 'marketing_rule':value=unwrapGenerated(await getMarketingConfigHistoryRule(id,apiRequestOptions()));break;
default:return invalid();
}
const r=object(value,['source','read_only','real_external_call_executed','item']);safety(r);
const result=item(kind,r.item);if(result.id!==id) invalid();return result;
}

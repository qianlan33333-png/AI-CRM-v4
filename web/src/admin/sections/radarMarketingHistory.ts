import { readHistoryPage, readHistoryDetail, type HistoryKind, type HistoryItem } from '../../api/radarMarketingHistory';
import { esc } from './util';

const titles: Record<HistoryKind,string> = {radar_click:'Radar 历史点击',marketing_config:'营销自动化历史配置',marketing_rule:'营销自动化历史规则'};
const labels:Record<string,string> = {
 id:'历史ID',source_id:'V1源ID',link_source_id:'V1链接ID',radar_link_id:'已关联V2历史链接',customer_id:'已关联V2客户',
 code:'原链接代码',raw_stage:'原阶段',source_channel:'原渠道',target_type_snapshot:'原目标类型',source_channel_snapshot:'原渠道快照',error_code:'原错误码',
 created_at:'原创建时间',updated_at:'原更新时间',automation_key:'原自动化标识',automation_name:'原名称',target_event:'原目标事件',channel_type:'原渠道类型',original_status:'V1状态（非当前执行状态）',
 do_not_start_after_hour:'原截止小时',config_id:'历史配置ID',config_source_id:'V1配置ID',questionnaire_source_id:'V1问卷ID（非V2）',question_source_id:'V1题目ID（非V2）',
 rule_code:'原规则代码',rule_name:'原规则名称',answer_match_type:'原匹配方式',score_delta:'原分值',segment_hint:'原分群提示',stage_hint:'原阶段提示',
 original_active:'V1启用标记（不启用V2自动化）',sort_order:'原排序'
};
const text=(v:unknown):string=>v===null?'NULL（未关联）':v===''?'（空）':esc(String(v));
const href=(kind:HistoryKind,id?:number):string=>{
 const params=new URLSearchParams(kind==='radar_click'?{click_history:'1'}:{marketing_config_history:'1',history_kind:kind});
 if(id!==undefined) params.set('history_id',String(id));
 return (kind==='radar_click'?'radar.html':'ai.html')+'?'+params.toString();
};
const facts=(v:HistoryItem):string=>Object.entries(v).map(([key,value])=>'<tr><th style="text-align:left;padding:8px">'+esc(labels[key]??key)+'</th><td style="padding:8px;white-space:pre-wrap">'+text(value)+'</td></tr>').join('');

export async function mountRadarMarketingHistory(stage:HTMLElement,options:{kind:string;historyID?:string}):Promise<void>{
 const kind=options.kind as HistoryKind;
 if(!Object.prototype.hasOwnProperty.call(titles,kind)) throw new Error('历史类型无效');
 const id=options.historyID===undefined?undefined:Number(options.historyID);
 if(id!==undefined && (!/^[1-9]\d*$/.test(options.historyID!) || !Number.isSafeInteger(id))) throw new Error('历史ID无效');
 stage.innerHTML='<section data-radar-marketing-history style="padding:20px;overflow:auto;flex:1"><h1>'+titles[kind]+'（只读）</h1><p>封存历史，不计入当前点击，不启用自动化，不触发任何外部效果。V1源ID不等于V2 ID；NULL明确表示未关联。</p>'+
 (kind==='radar_click'?'<a href="radar.html">返回 Radar</a>':'<a href="'+href('marketing_config')+'">历史配置</a> · <a href="'+href('marketing_rule')+'">历史规则</a> · <a href="ai.html">返回自动化</a>')+
 '<div data-history-content></div></section>';
 const section=stage.querySelector<HTMLElement>('[data-history-content]')!;
 const load=async(offset:number):Promise<void>=>{
  section.innerHTML='<p role="status">正在读取历史…</p>';
  try{
   if(id!==undefined){
    const value=await readHistoryDetail(kind,id);
    section.innerHTML='<p><a href="'+href(kind)+'">返回历史列表</a></p><table>'+facts(value)+'</table>';
    return;
   }
   const page=await readHistoryPage(kind,offset,20);
   section.innerHTML='<p>共 '+page.total+' 条</p>'+ (page.items.map(v=>'<details style="margin:12px 0"><summary>历史 #'+v.id+' · V1 #'+v.source_id+'</summary><table>'+facts(v)+'</table><a href="'+href(kind,v.id)+'">查看只读详情</a></details>').join('') || '<p>暂无历史记录</p>')+
    '<button data-history-prev '+(offset===0?'disabled':'')+'>上一页</button> <button data-history-next '+(offset+page.items.length>=page.total?'disabled':'')+'>下一页</button>';
   section.querySelector('[data-history-prev]')?.addEventListener('click',()=>{void load(Math.max(0,offset-20));});
   section.querySelector('[data-history-next]')?.addEventListener('click',()=>{void load(offset+20);});
  }catch(error){
   section.innerHTML='<p role="alert">'+esc(error instanceof Error?error.message:'历史读取失败')+'；未显示旧数据，也未回退 Mock。</p><button data-history-retry>重新读取</button>';
   section.querySelector('[data-history-retry]')?.addEventListener('click',()=>{void load(offset);});
  }
 };
 await load(0);
}

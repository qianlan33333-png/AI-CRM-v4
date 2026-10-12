import { request } from '../src/api/transport';
import { formatShanghaiDateTime } from './adminDateTime';

// This read-only dialog belongs to the existing questionnaire list. Images
// use only the answer-scoped admin endpoint and the current admin cookie.
export async function showSurveyAnswers(qid: number, sid?: number): Promise<void> {
  const dialog = document.createElement('dialog');
  dialog.dataset.surveyAnswers = '';
  dialog.style.cssText='width:min(820px,calc(100% - 48px));max-height:85vh;border:1px solid #DEE0E3;border-radius:12px;padding:20px;color:#1F2329;background:#fff;font:inherit;font-size:13px;';
  const header=document.createElement('div');header.style.cssText='display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:16px';
  const title=document.createElement('strong');title.textContent='问卷答卷';title.style.fontSize='16px';
  const close=document.createElement('button');close.textContent='关闭';close.onclick=()=>dialog.close();header.append(title,close);
  const status=document.createElement('p');status.setAttribute('role','status');
  const content=document.createElement('div');content.style.cssText='max-height:60vh;overflow:auto;';
  const previous=document.createElement('button');previous.textContent='上一页';const next=document.createElement('button');next.textContent='下一页';
  for(const button of [close,previous,next]) button.style.cssText='height:32px;padding:0 12px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;color:#646A73;font-size:13px;cursor:pointer';
  const footer=document.createElement('div');footer.style.cssText='display:flex;gap:12px;margin-top:16px';footer.append(previous,next);footer.hidden=!!sid;if(sid)footer.style.display='none';
  dialog.append(header,status,content,footer);document.body.append(dialog);dialog.onclose=()=>dialog.remove();dialog.showModal();
  let offset=0;let busy=false;
  async function load():Promise<void>{
    if(busy)return;busy=true;previous.disabled=next.disabled=true;status.textContent='正在读取答卷…';
    try{
      const response=await request(sid ? `/api/admin/questionnaires/${qid}/submissions/${sid}` : `/api/admin/questionnaires/${qid}/submissions?limit=20&offset=${offset}`);
      const raw=await response.json();
      const page=(sid ? {items:[raw.submission],total:1} : raw) as {items:{id:number;submitted_at:string;answers:{id:number;question_title:string;text_value_masked:string;selected_options:{option_text:string}[];attachment?:{file_name:string}}[]}[];total:number};
      if(!Array.isArray(page.items)||!Number.isSafeInteger(page.total))throw new Error('答卷响应不完整');
      content.replaceChildren();
      for(const item of page.items){
        if(!Number.isSafeInteger(item.id)||item.id<1)throw new Error('答卷响应不完整');
        const section=document.createElement('section');section.style.cssText='border:1px solid #DEE0E3;border-radius:8px;padding:16px;margin-bottom:12px';
        const heading=document.createElement('strong');heading.textContent=`#${item.id} · ${formatShanghaiDateTime(item.submitted_at)}`;section.append(heading);
        for(const answer of item.answers){
          const label=document.createElement('p');label.textContent=answer.question_title;label.style.cssText='font-weight:500;margin:14px 0 8px';section.append(label);
          if(answer.attachment && Number.isSafeInteger(answer.id) && answer.id>0){
            const path=`/api/admin/questionnaires/${qid}/submissions/${item.id}/answers/${answer.id}/attachment`;
            const link=document.createElement('a');link.href=path;link.target='_blank';link.rel='noopener';link.textContent=answer.attachment.file_name+' · 查看图片';
            const image=document.createElement('img');image.src=path;image.alt=answer.attachment.file_name;image.loading='lazy';image.style.cssText='display:block;max-width:160px;max-height:120px;object-fit:contain;border-radius:6px;margin-bottom:6px';
            section.append(image,link);
          }else{const value=document.createElement('div');value.textContent=answer.text_value_masked || answer.selected_options.map(option=>option.option_text).join('、') || '—';section.append(value);}
        };content.append(section);
      }
      status.textContent=page.total ? `共 ${page.total} 份 · 第 ${Math.floor(offset/20)+1} 页` : '暂无答卷';
      previous.disabled=!!sid || offset===0;next.disabled=!!sid || offset+page.items.length>=page.total;
    }catch(error){status.textContent=error instanceof Error?error.message:'答卷读取失败，请重试';content.replaceChildren();previous.disabled=offset===0;next.disabled=false;next.textContent='重试';}
    finally{busy=false;}
  }
  previous.onclick=()=>{offset=Math.max(0,offset-20);void load();};next.onclick=()=>{if(next.textContent!=='重试')offset+=20;next.textContent='下一页';void load();};await load();
}

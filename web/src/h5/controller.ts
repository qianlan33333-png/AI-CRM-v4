import { mergeShippingPrefill, mountShippingContact, emptyShippingContact, shippingContactError, type ShippingContact, type ShippingContactForm } from '../../v3/shared/ui/shippingContact';
import { showSurveyFieldDialog } from '../../v3/shared/ui/surveyFieldDialog';
import { localImageThumbnail } from '../../v3/shared/ui/localImageThumbnail';
import { imagePickerIcons } from '../../v3/shared/ui/imagePickerIcons';
import { PageBase, type Vals } from '../shared/ui/runtime';
import {
  completionAction,
  completionActionFromCarrier,
  readPublicSurvey,
  readSurveyResult,
  submitSurvey,
  surveyRead,
  type CompletionAction,
} from '../../v3/surveyPublicApi';
import { toast } from '../shared/ui/feedback';
import { ApiError } from '../api/transport';
import { formatShanghaiDateTime } from '../../v3/adminDateTime';

import type {
  SurveyPublicDefinition,
  SurveyPublicQuestion,
  SurveyPublicResult,
  SurveyPublicSubmissionAnswer,
} from '../../v3/generated/survey-public';

// Use the same current public contract as the HTTP transport. The legacy
// health schema omitted Other and needed handwritten field extensions.
type V3SurveyQuestion = SurveyPublicQuestion;
type V3SurveyDefinition = SurveyPublicDefinition & {
  authorizationContext?: string;
};
type V3SurveyResult = SurveyPublicResult;
type V3SurveySubmissionAnswer = SurveyPublicSubmissionAnswer;

const validID = (value: number): boolean => Number.isSafeInteger(value) && value > 0;
const validToken = (value: string): boolean => /^[A-Za-z0-9_-]{43}$/.test(value);
const SUPPORTED_TYPES = ['single_choice', 'multi_choice', 'textarea', 'mobile', 'redemption_code', 'attachment', 'shipping_address'];

type SubmissionResumeDraft = {
  schema: 1;
  savedAt?: number;
  pendingSubmit?: boolean;
  hadImages?: boolean;
  definition: V3SurveyDefinition;
  answers: [number, number[]][];
  textAnswers: [number, string][];
  submissionKey: string;
  questionIndex: number;
};

export class H5Controller extends PageBase {
  private definition: V3SurveyDefinition | null = null;
  private result: V3SurveyResult | null = null;
  private answers = new Map<number, number[]>();
  private textAnswers = new Map<number, string>();
  private shippingForms = new Map<number, ShippingContactForm>();
  private shippingTouched = new Map<number, Set<string>>();
  private images = new Map<number, { file: File; url: string }>();
  private previewRequests = new Map<number, symbol>();
  private unsupportedTypes = new Set<number>();
  private questionIndex = 0;
  private loading = false;
  private submitting = false;
  private error = '';
  private submissionKey = '';
  private resultToken = '';
  private submitted = false;
	private slug = '';
  private done = false;
  private definitionStale = false;
  private doneAction: CompletionAction = { type: 'default' };

  constructor(readonly page: string) { super(); }

  async init(): Promise<void> {
	if (this.page === 'auth') {
		this.slug = new URLSearchParams(location.search).get('slug') || '';
		if (!/^[a-z0-9](?:[a-z0-9-]{0,126}[a-z0-9])?$/.test(this.slug)) this.error = '缺少有效公开问卷 slug';
		else if (new URLSearchParams(location.search).has('oauth_error')) this.error = '微信授权未完成，请重试';
		else if (/MicroMessenger/i.test(navigator.userAgent || '')) {
			try {
				const { session, body } = await surveyRead(async (signal) => {
					const session = await fetch(`/api/h5/surveys/session?slug=${encodeURIComponent(this.slug)}`, { signal, credentials: 'same-origin', headers: { Accept: 'application/json' } });
					const body = session.ok ? await session.json() as { display?: string; submitted?: unknown; completion_action?: unknown } : undefined;
					return { session, body };
				});
				if (session.ok) {
					try { sessionStorage.removeItem('survey.oauth:' + this.slug); } catch {}
            if (body!.submitted === true) {
              this.complete(completionAction(body!.completion_action));
              return;
            }
					location.replace(`/h5/${body!.display === 'one' ? 'one' : 'all'}.html?slug=${encodeURIComponent(this.slug)}`);
				} else if (session.status === 401) this.startOAuth(false);
				else if (session.status === 409) this.error = '当前微信身份存在冲突，请联系管理员处理后再填写';
				else if (session.status === 503) this.error = '微信授权暂不可用，请稍后再试';
				else this.error = '微信授权状态暂不可用，请重试';
			} catch { this.error = '微信授权状态读取失败，请检查网络后重试'; }
		}
		this.refresh();
		return;
	}
	if (!['all', 'one', 'result', 'done'].includes(this.page)) return;
    if (this.loading || this.submitting) return;
    this.loading = true;
    this.definitionStale = false;
    this.error = '';
    this.unsupportedTypes = new Set();
    this.refresh();
    let resumeSubmit = false;
    const query = new URLSearchParams(location.search);
    const returningFromAuthorization = query.get('resume') === 'submit';
    try {
      if (this.page === 'result') {
        const query = new URLSearchParams(location.search);
        this.resultToken ||= new URLSearchParams((location.hash || '').slice(1)).get('result_token') || query.get('result_token') || '';
        if (!validToken(this.resultToken)) throw new Error('缺少有效结果凭据，无法查询提交结果');
        const result = await readSurveyResult(this.resultToken) as V3SurveyResult;
        if (!validID(result.submission_id) || !validID(result.definition_version) ||
            !Number.isFinite(Date.parse(result.submitted_at)) || result.local_only !== true || result.external_executed !== false) {
          throw new Error('提交结果响应不完整，未确认结果');
        }
        this.result = result;
      } else {
		const slug = new URLSearchParams(location.search).get('slug') || '';
		if (!/^[a-z0-9](?:[a-z0-9-]{0,126}[a-z0-9])?$/.test(slug)) throw new Error('缺少有效公开问卷 slug，不能填写或提交');
		this.slug = slug;
        if (this.page === 'done') {
          const body = await surveyRead(async (signal) => {
            const session = await fetch(`/api/h5/surveys/session?slug=${encodeURIComponent(slug)}`, { signal, credentials: 'same-origin', headers: { Accept: 'application/json' } });
            if (!session.ok) throw new Error('问卷完成状态暂不可读取');
            return await session.json() as { submitted?: unknown; completion_action?: unknown };
          });
          if (body.submitted !== true) throw new Error('当前问卷尚未提交');
          this.doneAction = completionAction(body.completion_action);
          if (this.doneAction.type === 'redirect') {
            this.complete(this.doneAction);
            return;
          }
          this.done = true;
        } else {
          const definition = await readPublicSurvey(slug) as V3SurveyDefinition;
          this.validateDefinition(definition, slug);
          const draft = this.readResumeDraft() || this.snapshotDraft();
          this.definition = definition;
          this.answers.clear();
          this.textAnswers.clear();
          this.images.clear();
          this.previewRequests.clear();
          this.submissionKey = '';
          this.questionIndex = 0;
          document.title = definition.title;
          if (draft) {
            if (!definition.authorizationContext || definition.authorizationContext !== draft.definition.authorizationContext) {
              this.clearResumeDraft();
              this.error = '重新授权的微信身份与原填写身份不同，请用原微信重新打开问卷；旧答案未提交。';
            } else if (definition.version !== draft.definition.version) {
              this.clearResumeDraft();
              this.error = '问卷内容已更新，旧答案尚未提交。请核对当前题目后重新填写并提交。';
            } else {
              this.restoreResumeDraft(draft);
              resumeSubmit = returningFromAuthorization && !query.has('oauth_error') && draft.pendingSubmit !== false && !draft.hadImages;
            }
          }
          if (!draft) void this.prefillContact(definition);
          const expectedPage = definition.answer_display_mode === 'one_by_one' ? 'one' : 'all';
          if (this.page !== expectedPage) {
            this.saveDraft(resumeSubmit);
            const destination = new URL(location.href);
            destination.pathname = `/h5/${expectedPage}.html`;
            location.replace(destination.href);
            return;
          }
        }
      }
    } catch (error) {
	  if (this.slug && isAlreadySubmitted(error)) {
        this.complete(completionActionFromCarrier(error.details));
        return;
      }
	  if (error instanceof ApiError && error.status === 401 && this.slug) {
		const draft = this.readResumeDraft();
		if (draft) {
          this.validateDefinition(draft.definition, this.slug);
          this.definition = draft.definition;
          document.title = draft.definition.title;
          this.restoreResumeDraft(draft);
          this.error = '';
          if (returningFromAuthorization && !query.has('oauth_error')) this.authorizationRetryNotice();
          return;
        }
		location.replace(`/q/${encodeURIComponent(this.slug)}`);
		return;
	  }
      this.definition = null;
      this.result = null;
      this.error = error instanceof Error ? error.message : '问卷读取失败，请重试';
    } finally {
      this.loading = false;
      this.refresh(returningFromAuthorization);
    }
    if (returningFromAuthorization) {
      const cleanURL = new URL(location.href);
      cleanURL.searchParams.delete('resume');
      cleanURL.searchParams.delete('oauth_error');
      history.replaceState(null, '', cleanURL);
    }
    if (resumeSubmit) await this.submit(true);
  }

  private complete(action: CompletionAction): void {
    this.clearResumeDraft();
    this.submitted = true;
    const destination = action.type === 'redirect'
      ? action.redirect_url
      : `/h5/done.html?slug=${encodeURIComponent(this.slug)}`;
    location.replace(destination);
  }

  private startOAuth(retry: boolean): void {
    if (!/MicroMessenger/i.test(navigator.userAgent || '') || !this.slug) return;
    try {
      const key = 'survey.oauth:' + this.slug;
      if (!retry && sessionStorage.getItem(key)) { this.error = '微信授权未完成，请重试'; return; }
      sessionStorage.setItem(key, 'started');
    } catch { this.error = '无法保存授权状态，请允许浏览器存储后重试'; return; }
    location.replace(`/api/h5/surveys/oauth/start?slug=${encodeURIComponent(this.slug)}`);
  }

  private refresh(revealError = false): void {
    this.__render?.();
    this.shippingForms.clear();
    for(const container of document.querySelectorAll<HTMLElement>('[data-shipping-question-id]')) {
      const id=Number(container.dataset.shippingQuestionId);
      const value=this.shippingAnswer(id)||emptyShippingContact();
      this.shippingForms.set(id,mountShippingContact(container,value,this.definition?.region_options||[],next=>{
        this.textAnswers.set(id,Object.values(next).some(Boolean)?JSON.stringify(next):'');this.submissionKey='';this.saveDraft();
      },this.contactTouched(id)));
    }
    if (revealError && this.error) {
      // Reuse the existing feedback layer near the submit action. Scrolling
      // alone may be unavailable or ineffective in an embedded mobile view.
      toast(this.error, true);
      try { document.querySelector<HTMLElement>('#screen [data-h5-error]')?.scrollIntoView?.({ block: 'center' }); } catch {}
    }
  }

  private authorizationRetryNotice(): void {
    toast('微信登录状态未能保持，请再次点击提交重试，答案已保留。', true);
  }

  private contactTouched(id:number):Set<string>{let fields=this.shippingTouched.get(id);if(!fields){fields=new Set<string>();this.shippingTouched.set(id,fields);}return fields;}

  private shippingAnswer(id:number): ShippingContact | undefined {try {const raw=this.textAnswers.get(id);return raw?JSON.parse(raw):undefined;}catch{return undefined;}}

  private async prefillContact(definition:V3SurveyDefinition):Promise<void> {
    if(!definition.questions.some(q=>q.contact_field))return;
    try {
      const response=await surveyRead(signal=>fetch(`/api/h5/surveys/session?slug=${encodeURIComponent(definition.slug)}`,{signal,credentials:'same-origin',headers:{Accept:'application/json'}}));
      if(!response.ok)return;
      const body=await response.json();if(this.definition!==definition || this.submitted || this.submitting)return;
      for(const q of definition.questions) {
        const value=body.contact_prefill?.[q.id];if(!q.contact_field || !value)continue;
        if(q.contact_field==='contact_phone' && typeof value.text_value==='string' && !this.textAnswers.has(q.id)) {
          const input=document.querySelector<HTMLInputElement>(`[data-text-question-id="${q.id}"]`);if(input?.value)continue;
          this.textAnswers.set(q.id,value.text_value);if(input)input.value=value.text_value;
        }
        if(q.contact_field==='shipping_contact' && value.shipping_address) {
          const form=this.shippingForms.get(q.id);
          const next=form ? form.prefill(value.shipping_address) : mergeShippingPrefill(this.shippingAnswer(q.id)||emptyShippingContact(),value.shipping_address,this.contactTouched(q.id));
          this.textAnswers.set(q.id,JSON.stringify(next));
        }
      }
    } catch { /* Prefill is optional; manual entry remains usable. */ }
  }

  private captureVisibleText(): void {
    if (!this.definition) return;
    const visible = this.definition.answer_display_mode === 'one_by_one'
      ? this.definition.questions.slice(this.questionIndex, this.questionIndex + 1)
      : this.definition.questions;
    const questions = new Map(visible.map((question) => [question.id, question]));
    let changed = false;
    for(const [id,form] of this.shippingForms){const value=form.read();const raw=Object.values(value).some(Boolean)?JSON.stringify(value):'';if(raw!==(this.textAnswers.get(id)||'')){this.textAnswers.set(id,raw);changed=true;}}
    for (const input of document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement>('#screen input[data-text-question-id], #screen textarea[data-text-question-id]')) {
      const question = questions.get(Number(input.dataset.textQuestionId));
      if (!question || !this.supported(question) ||
          (!['mobile', 'textarea', 'redemption_code'].includes(question.type) && !this.selectedOther(question))) continue;
      if (input.value !== (this.textAnswers.get(question.id) || '')) {
        this.textAnswers.set(question.id, input.value);
        changed = true;
      }
    }
    // Native/history-restored input values need not have emitted input/change.
    // Capture before a choice rerender, navigation or submission removes them.
    if (changed) { this.submissionKey = ''; this.saveDraft(); }
  }

  private resumeDraftKey(): string { return 'survey.submit-resume:' + this.slug; }

  private clearResumeDraft(): void {
    try { sessionStorage.removeItem(this.resumeDraftKey()); } catch {}
  }

  private readResumeDraft(): SubmissionResumeDraft | null {
    try {
      const raw = sessionStorage.getItem(this.resumeDraftKey());
      if (!raw) return null;
      const draft = JSON.parse(raw) as SubmissionResumeDraft;
      if (draft.schema !== 1 || draft.definition?.slug !== this.slug ||
          !/^[a-f0-9]{64}$/.test(draft.definition.authorizationContext || '') ||
          !(draft.submissionKey === '' || validToken(draft.submissionKey)) ||
          (draft.pendingSubmit !== undefined && typeof draft.pendingSubmit !== 'boolean') ||
          (draft.savedAt !== undefined && (!Number.isFinite(draft.savedAt) || draft.savedAt > Date.now() || Date.now() - draft.savedAt > 24 * 60 * 60 * 1000)) || !Number.isSafeInteger(draft.questionIndex) || draft.questionIndex < 0 ||
          !Array.isArray(draft.answers) || !draft.answers.every((item) => Array.isArray(item) && item.length === 2 && validID(item[0]) && Array.isArray(item[1]) && item[1].every(validID)) ||
          !Array.isArray(draft.textAnswers) || !draft.textAnswers.every((item) => Array.isArray(item) && item.length === 2 && validID(item[0]) && typeof item[1] === 'string')) {
        this.clearResumeDraft();
        return null;
      }
      const unsupported = this.unsupportedTypes;
      try { this.validateDefinition(draft.definition, this.slug); }
      finally { this.unsupportedTypes = unsupported; }
      const questions = new Map(draft.definition.questions.map((question) => [question.id, question]));
      const choiceIDs = new Set<number>();
      const textIDs = new Set<number>();
      for (const [id, options] of draft.answers) {
        const question = questions.get(id);
        if (!question || !['single_choice', 'multi_choice'].includes(question.type) || choiceIDs.has(id) ||
            new Set(options).size !== options.length || options.some((optionID) => !question.options.some((option) => option.id === optionID))) {
          this.clearResumeDraft(); return null;
        }
        choiceIDs.add(id);
      }
      for (const [id] of draft.textAnswers) {
        const question = questions.get(id);
        if (!question || textIDs.has(id) || (!['textarea', 'mobile', 'redemption_code', 'shipping_address'].includes(question.type) &&
            !this.selectedOther(question, new Map(draft.answers).get(id) || []))) {
          this.clearResumeDraft(); return null;
        }
        textIDs.add(id);
      }
      return draft;
    } catch { this.clearResumeDraft(); return null; }
  }

  private snapshotDraft(pendingSubmit = false): SubmissionResumeDraft | null {
    if (!this.definition || !/^[a-f0-9]{64}$/.test(this.definition.authorizationContext || '')) return null;
    return { schema: 1, savedAt: Date.now(), pendingSubmit: this.images.size ? false : pendingSubmit, hadImages: this.images.size > 0, definition: this.definition,
      answers: [...this.answers], textAnswers: [...this.textAnswers],
      submissionKey: this.submissionKey, questionIndex: this.questionIndex };
  }

  private saveDraft(pendingSubmit = false): boolean {
    const draft = this.snapshotDraft(pendingSubmit);
    if (!draft) return false;
    try { sessionStorage.setItem(this.resumeDraftKey(), JSON.stringify(draft)); return true; }
    catch { return false; } // Ordinary filling/submission still works without storage.
  }

  private restoreResumeDraft(draft: SubmissionResumeDraft): void {
    this.answers = new Map(draft.answers);
    this.textAnswers = new Map(draft.textAnswers);
    this.submissionKey = draft.hadImages ? '' : draft.submissionKey;
    if (draft.hadImages) this.error = '已恢复文字答案，请重新选择图片后提交。';
    this.questionIndex = Math.min(draft.questionIndex, (this.definition?.questions.length || 1) - 1);
  }

  private resumeAuthorization(): void {
    if (!this.definition) return;
    if (!/MicroMessenger/i.test(navigator.userAgent || '')) {
      this.error = '请在微信中打开原问卷，完成授权后提交。当前答案仍在本页。';
      this.refresh(true);
      return;
    }
    if (!/^[a-f0-9]{64}$/.test(this.definition.authorizationContext || '')) {
      this.error = '暂时无法安全保存授权恢复状态，请保留当前页面并稍后重试。';
      this.refresh(true);
      return;
    }
    this.submissionKey ||= this.newSubmissionKey();
    if (!this.saveDraft(true)) {
      this.error = '浏览器未能保存答案，尚未跳转。请保留当前页面并允许浏览器存储后重试。';
      this.refresh(true);
      return;
    }
    location.assign(`/api/h5/surveys/oauth/start?slug=${encodeURIComponent(this.slug)}&resume=submit`);
  }

  private validateDefinition(definition: V3SurveyDefinition, slug: string): void {
    if (!definition || definition.slug !== slug || !validID(definition.id) || !validID(definition.version) ||
        typeof definition.title !== 'string' || !definition.title || typeof definition.description !== 'string' ||
        !['all_in_one', 'one_by_one'].includes(definition.answer_display_mode) ||
        !Array.isArray(definition.questions) || !definition.questions.length) throw new Error('公开问卷定义不完整');
    const ids = new Set<number>();
    this.unsupportedTypes = new Set();
    for (const question of definition.questions) {
      if (!validID(question.id) || ids.has(question.id) || typeof question.title !== 'string' || !question.title) {
        throw new Error('问卷题目响应不完整');
      }
      ids.add(question.id);
      // 契约外题型不整卷失败：降级为禁用题卡，由 renderVals 标注且不参与作答/提交。
      if (!SUPPORTED_TYPES.includes(question.type)) {
        this.unsupportedTypes.add(question.id);
        continue;
      }
      if (typeof question.required !== 'boolean' || !Array.isArray(question.options)) {
        throw new Error('问卷题目响应不完整');
      }
      if (['single_choice', 'multi_choice'].includes(question.type) && (!question.options.length ||
          !Number.isInteger(question.minimum_selections) || !Number.isInteger(question.maximum_selections) ||
          question.minimum_selections < 0 || question.maximum_selections < 1 ||
          question.minimum_selections > question.maximum_selections || question.maximum_selections > question.options.length ||
		  (question.required && question.minimum_selections === 0) || (question.type === 'single_choice' && question.maximum_selections !== 1))) {
        throw new Error('问卷题目超出当前单选/多选契约');
      }
	  if (['textarea', 'mobile', 'redemption_code', 'attachment', 'shipping_address'].includes(question.type) && question.options.length) throw new Error('输入题不应包含选项');
      const options = new Set<number>();
      for (const option of question.options) {
        if (!validID(option.id) || options.has(option.id) || typeof option.option_text !== 'string' || !option.option_text) throw new Error('问卷选项响应不完整');
        options.add(option.id);
      }
    }
  }

  private supported(question: V3SurveyQuestion): boolean {
    return !this.unsupportedTypes.has(question.id);
  }

  private selectedOther(question: V3SurveyQuestion, selected = this.answers.get(question.id) || []) {
    return question.options.find((option) => option.is_other && selected.includes(option.id));
  }

  private unsupportedNotice(): string {
    return this.definition && this.unsupportedTypes.size
      ? '本问卷包含当前端暂不支持的题型，相关题目已禁用且不参与提交；其余题目可正常作答'
      : '';
  }

  private select(question: V3SurveyQuestion, optionID: number): void {
    if (this.submitting || this.submitted || !this.supported(question)) return;
    this.captureVisibleText();
    const previous = this.answers.get(question.id) || [];
    const next = question.type === 'single_choice' ? [optionID]
      : previous.includes(optionID) ? previous.filter((id) => id !== optionID) : [...previous, optionID];
    next.sort((a, b) => a - b);
    if (next.join(',') !== previous.join(',')) this.submissionKey = '';
    this.answers.set(question.id, next);
    // The Owner rejects supplemental text when Other is no longer selected.
    if (!this.selectedOther(question, next)) this.textAnswers.delete(question.id);
    this.saveDraft();
    if (!this.definitionStale) this.error = '';
    this.refresh();
  }

  private setText(question: V3SurveyQuestion, event: Event): void {
    if (this.submitting || this.submitted || !this.supported(question)) return;
    const value = String((event.target as HTMLInputElement | HTMLTextAreaElement).value || '');
    if (value !== (this.textAnswers.get(question.id) || '')) this.submissionKey = '';
    this.textAnswers.set(question.id, value);
    this.saveDraft();
    if (this.error && !this.definitionStale) {
      this.error = '';
      // Remove only stale feedback. A full render here remounts the active
      // textarea and can interrupt mobile focus/IME composition.
      document.querySelector('#screen [data-h5-error]')?.remove();
    }
  }

  private questionError(question: V3SurveyQuestion): string {
    if (!this.supported(question)) return '';
    if(question.type==='shipping_address') return shippingContactError(this.shippingAnswer(question.id),question.required);
    if (question.type === 'attachment') return question.required && !this.images.has(question.id) ? `「${question.title}」请上传图片` : '';
    if (question.type === 'textarea' || question.type === 'mobile' || question.type === 'redemption_code') {
      const value = this.textAnswers.get(question.id) || '';
      if (!value && !question.required) return '';
      if (question.type === 'redemption_code' && !value.trim()) return '请输入兑换码';
      if (!value || question.required && !value.trim()) return `「${question.title}」请填写`;
      if (question.type === 'mobile' && !/^1[3-9][0-9]{9}$/.test(value)) return `「${question.title}」请输入11位中国大陆手机号`;
      const length = [...value].length;
      if (length < (question.minimum_length || 0) || length > (question.maximum_length || 10000)) return `「${question.title}」长度不符合要求`;
      return '';
    }
    const count = (this.answers.get(question.id) || []).length;
    if (count === 0 && !question.required) return '';
    if (count < question.minimum_selections || count > question.maximum_selections) {
      return `「${question.title}」请选择 ${question.minimum_selections}–${question.maximum_selections} 项`;
    }
    const other = this.selectedOther(question);
    if (other) {
      const text = this.textAnswers.get(question.id) || '';
      if (!text.trim()) return `「${question.title}」请填写其他说明`;
      if ([...text].length > other.other_max_length) return `「${question.title}」其他说明最多 ${other.other_max_length} 字`;
    }
    return '';
  }

  private async setImage(question: V3SurveyQuestion, event: Event): Promise<void> {
    if (this.submitting || this.submitted) return;
    this.captureVisibleText();
    const file = (event.target as HTMLInputElement).files?.[0];
    const selectedToast = document.getElementById('fb-toast');
    if (selectedToast?.textContent?.startsWith('已选择：')) selectedToast.hidden = true;
    if (!file) return;
    let error = '';
    if (file.size >= 2097152) error = '图片必须小于 2MB';
    else if (!['image/jpeg', 'image/png', 'image/gif'].includes(file.type)) error = '请上传 JPG、PNG 或 GIF 图片';
    if (error) { showSurveyFieldDialog(error, question.id); (event.target as HTMLInputElement).value=''; return; }
    const request = Symbol();
    const definition = this.definition;
    this.previewRequests.set(question.id, request);
    this.refresh();
    try {
      const url = await localImageThumbnail(file);
      if (this.definition !== definition || this.previewRequests.get(question.id) !== request || this.submitted) return;
      this.images.set(question.id, { file, url });
      this.submissionKey = ''; this.error = ''; this.saveDraft();
    } catch {
      if (this.definition === definition && this.previewRequests.get(question.id) === request) {
        showSurveyFieldDialog('图片无法预览，请重新选择有效的 JPG、PNG 或 GIF 图片', question.id);
      }
    } finally {
      if (this.previewRequests.get(question.id) === request) {
        this.previewRequests.delete(question.id);
        this.refresh();
      }
    }
  }
  private removeImage(question: V3SurveyQuestion): void {
    if (this.submitting || this.submitted) return;
    this.captureVisibleText();
    this.previewRequests.delete(question.id);
    this.images.delete(question.id);this.submissionKey='';this.saveDraft();this.refresh(true);
  }
  private async submit(afterAuthorization = false): Promise<void> {
    if (!this.definition || this.submitting || this.submitted) return;
    if (this.previewRequests.size) {
      showSurveyFieldDialog('正在生成图片预览，请稍候', this.previewRequests.keys().next().value!);
      return;
    }
    if (this.definitionStale) { this.refresh(true); return; }
    this.captureVisibleText();
    const invalid = this.definition.questions.find(question => this.questionError(question));
    this.error = invalid ? this.questionError(invalid) : '';
    if (this.error) { if (invalid) this.questionIndex = this.definition.questions.indexOf(invalid); this.refresh(true); if (invalid && ['redemption_code','attachment'].includes(invalid.type)) showSurveyFieldDialog(this.error, invalid.id); return; }
    let fieldFeedback: { message: string; id?: number } | undefined;
    const answers: V3SurveySubmissionAnswer[] = this.definition.questions
      .filter((question) => this.supported(question) && ((this.answers.get(question.id) || []).length > 0 || (this.textAnswers.get(question.id) || '') !== '' || this.images.has(question.id)))
      .map((question) => ({ question_id: question.id, option_ids: [...(this.answers.get(question.id) || [])], ...(question.type==='shipping_address'?{shipping_address:this.shippingAnswer(question.id)}:{text_value:this.textAnswers.get(question.id)||undefined}), ...(this.images.has(question.id) ? {attachment:true} : {}) }));
    this.submitting = true;
    this.refresh();
    // Match the route's 120-second image / 15-second JSON wait budget. A lost response remains
    // outcome-unknown: release the UI lock, retain the original key, and let
    // the user retry rather than starting a second logical submission.
    try {
      // One key per unchanged answer set in this filling lifecycle. Unknown
      // network outcomes retry the same request; editing answers clears the key.
      this.submissionKey ||= this.newSubmissionKey();
      this.saveDraft();
      const receipt = await surveyRead((signal) => submitSurvey(this.definition!.slug, { version: this.definition!.version, submission_key: this.submissionKey, answers }, signal, new Map([...this.images].map(([id,value])=>[id,value.file]))), '提交结果暂未确认，答案与图片已保留；未修改内容时可安全重试。', this.images.size ? 120_000 : 15_000);
      this.complete(receipt.completionAction);
    } catch (error) {
      if (isAlreadySubmitted(error)) this.complete(completionActionFromCarrier(error.details));
      else if (error instanceof ApiError && error.status === 401) {
        this.error = '';
        if (!afterAuthorization) this.resumeAuthorization();
        else this.authorizationRetryNotice();
      }
      else if (error instanceof ApiError && error.details && typeof error.details === 'object' &&
               (String((error.details as { code?: string }).code).startsWith('redemption_code_') || String((error.details as { code?: string }).code).startsWith('attachment_') || (error.details as { code?: string }).code === 'submission_payload_conflict')) {
        const field = error.details as { message?: string; question_id?: number };
        this.error = field.message || '提交未完成，请检查后重试';
        fieldFeedback = { message: this.error, id: field.question_id };
        const index = this.definition.questions.findIndex(question => question.id === field.question_id);
        if (index >= 0) this.questionIndex = index;
      }
      else if (error instanceof ApiError && error.status === 409 &&
               ((error.details as { code?: unknown } | null)?.code === 'definition_version_conflict' ||
                (error.details as { error?: unknown } | null)?.error === 'definition_version_conflict')) {
        this.definitionStale = true;
        this.error = '问卷内容已更新，当前答案尚未提交。请点击重试读取最新题目后重新填写。';
      }
      else this.error = error instanceof Error ? error.message : '提交失败；未修改答案时可安全重试';
    } finally {
      this.submitting = false;
      this.refresh(true);
      if (fieldFeedback) showSurveyFieldDialog(fieldFeedback.message, fieldFeedback.id);
    }
  }

  private newSubmissionKey(): string {
    return btoa(String.fromCharCode(...crypto.getRandomValues(new Uint8Array(32))))
      .replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  }

  private move(delta: number): void {
    if (!this.definition || this.submitting || this.submitted) return;
    this.captureVisibleText();
    if (delta > 0 && !this.definitionStale) {
      this.error = this.questionError(this.definition.questions[this.questionIndex]);
      if (this.error) { this.refresh(true); return; }
    }
    this.questionIndex = Math.max(0, Math.min(this.definition.questions.length - 1, this.questionIndex + delta));
    this.saveDraft();
    if (!this.definitionStale) this.error = '';
    this.refresh();
  }

  private blocked(action: string): void {
    toast(`后端能力未就绪：${action}不可执行；未发起任何外部请求`, true);
  }

  /** 纯本地出口：回退上一页，无历史时回到 H5 屏幕列表；不发任何请求。 */
  private closePage(): void {
    if (window.history.length > 1) {
      window.history.back();
      return;
    }
    location.href = 'index.html';
  }

  renderVals(): Vals {
    const definition = this.definition;
    const stepMode = definition?.answer_display_mode === 'one_by_one';
    const questions = definition?.questions || [];
    const visible = stepMode ? questions.slice(this.questionIndex, this.questionIndex + 1) : questions;
    const ready = !!definition && !this.loading && !this.submitted;
    const current = visible[0];
    const errorText = this.error || this.unsupportedNotice();
    const assessment = (this.result?.assessment_result || {}) as Record<string, any>;
    const dimensions = Array.isArray(assessment.dimensions) ? assessment.dimensions : [];
    const legacyNext = (): void => {
      if (!definition || this.questionIndex < questions.length - 1) this.move(1);
      else void this.submit();
    };
    return {
      loading: this.loading, error: errorText, errorEmpty: !errorText, ready, result: this.result,
	  isAssessmentResult: this.result?.mode === 'assessment', resultTitle: this.result?.questionnaire_title || '问卷结果', totalScore: Number(this.result?.total_score || 0), overallTitle: assessment.overall_level?.title || '已完成', overallSummary: assessment.overall_level?.summary || '', dimensions,
      resultTime: this.result ? formatShanghaiDateTime(this.result.submitted_at) : '',
      notWechatUA: !/MicroMessenger/i.test(navigator.userAgent || ''),
	  authRetry: !!this.error && /MicroMessenger/i.test(navigator.userAgent || ''),
	  wechatUA: /MicroMessenger/i.test(navigator.userAgent || ''),
      submitted: this.submitted,
      done: this.done,
      leadQR: this.doneAction.type === 'lead_qr' ? this.doneAction.lead_qr : null,
      doneTitle: this.doneAction.type === 'lead_qr' ? (this.doneAction.lead_qr.title || '收到你的问卷') : '收到你的问卷',
      doneSubtitle: this.doneAction.type === 'lead_qr' ? (this.doneAction.lead_qr.subtitle || '长按识别二维码，继续咨询') : '',
      title: definition?.title || '公开问卷', description: definition?.description || '',
      progress: stepMode ? `第 ${this.questionIndex + 1} / ${questions.length} 题` : `共 ${questions.length} 题`,
      canPrevious: ready && stepMode && this.questionIndex > 0 && !this.submitting,
      canNext: ready && stepMode && this.questionIndex < questions.length - 1 && !this.submitting,
      canSubmit: ready && (!stepMode || this.questionIndex === questions.length - 1) && !this.submitting,
      submitting: this.submitting,
      canRetry: !this.loading && !this.submitting && !!this.error && (!definition || this.definitionStale),
	  blockedReason: this.page === 'auth'
		? (this.error || (/MicroMessenger/i.test(navigator.userAgent || '') ? '授权仅用于识别本次问卷所属客户，不会发送短信。' : '请复制当前链接并在微信内打开。'))
        : '后端能力未就绪：当前页面没有可用的报名、支付、续费、二维码或完成状态契约；未执行任何外部操作。',
      questions: visible.map((question) => {
        const supported = this.supported(question);
        const isText = question.type === 'textarea' || question.type === 'mobile' || question.type === 'redemption_code';
        const other = this.selectedOther(question);
        return {
          id: question.id, title: question.title, required: question.required ? '（必答）' : '（选答）',
          supported, unsupported: !supported,
          hasOther: !!other, otherPlaceholder: other?.other_placeholder || '请补充其他说明',
          isText, isRedemption: question.type === 'redemption_code', isMobile: question.type === 'mobile', isTextarea: question.type === 'textarea', isShipping:question.type==='shipping_address', isChoice: !isText && question.type !== 'attachment' && question.type!=='shipping_address', isAttachment: question.type === 'attachment', hasImage:this.images.has(question.id), noImage:!this.images.has(question.id), imageLoading:this.previewRequests.has(question.id), imageLabel:this.images.has(question.id) ? '重新选择图片' : '选择图片', imageURL:this.images.get(question.id)?.url || '', iconPlus:imagePickerIcons.plus, iconClose:imagePickerIcons.close, iconSelected:imagePickerIcons.selected, selectImage:(event:Event)=>this.setImage(question,event), removeImage:()=>this.removeImage(question), inputType: question.type === 'mobile' ? 'tel' : 'text', inputValue: this.textAnswers.get(question.id) || '', placeholder: question.type === 'mobile' ? '请输入11位中国大陆手机号' : question.placeholder_text || '请输入', input: (event: Event) => this.setText(question, event),
          hint: !supported ? '当前端不支持该题型，本题不参与作答与提交'
            : question.type === 'single_choice' ? '单选' : question.type === 'multi_choice' ? `多选，限选 ${question.maximum_selections} 项` : question.type === 'mobile' ? '11位中国大陆手机号' : question.type === 'redemption_code' ? '兑换码仅在提交时验证，只能使用一次' : question.type === 'shipping_address' ? '' : question.type === 'attachment' ? '一张 JPG、PNG 或 GIF 图片，小于 2MB' : '文本回答',
          options: supported && !isText && question.type !== 'attachment' ? question.options.map((option) => {
            const selected = (this.answers.get(question.id) || []).includes(option.id);
            return { id: option.id, text: option.option_text, selected, mark: selected ? '✓' : '○',
              style: { background: selected ? '#EFF4FF' : '#fff', borderColor: selected ? '#3370ff' : '#DEE0E3' },
              pick: () => this.select(question, option.id) };
          }) : [],
        };
      }),
      qProgressText: stepMode && questions.length ? `第 ${this.questionIndex + 1} / ${questions.length} 题` : '尚未读取题目',
      qPct: stepMode && questions.length ? Math.round(((this.questionIndex + 1) / questions.length) * 100) : 0,
      qTitle: current?.title || '问卷题目',
      act: {
        submit: () => { void this.submit(); }, previous: () => this.move(-1), next: () => this.move(1), retry: () => { void this.init(); },
		authContinue: () => {
			this.startOAuth(true);
		}, submitAll: () => { void this.submit(); }, prevQ: () => this.move(-1), nextQ: legacyNext,
        signup: () => this.blocked('报名'), pay: () => this.blocked('支付'), renew: () => this.blocked('续费'),
        addWx: () => this.blocked('添加企微账号'), close: () => this.closePage(),
      },
    };
  }
}

function isAlreadySubmitted(error: unknown): error is ApiError {
  if (!(error instanceof ApiError) || error.status !== 409 || !error.details || typeof error.details !== 'object') return false;
  const details = error.details as { code?: unknown; error?: unknown };
  return details.code === 'already_submitted' || details.error === 'already_submitted';
}

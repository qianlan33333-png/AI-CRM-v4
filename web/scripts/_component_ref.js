class Component extends DCLogic {
  state = { mode: null, page: 'customers', rail: null, cstep: 1, modal: null, toast: null, radarCur: 1, radarType: 'link', radarMedia: null, upPct: null, aiCur: 7, aiShown: 3, rcDrawer: null, fView: 0, fPanel: null, saving: false };

  get mode() { return this.state.mode ?? (this.props.mode === '画板' ? 'board' : 'proto'); }
  get rail() { return this.state.rail ?? (this.props.railCollapsed === true); }

  chip(tone) {
    const m = { ok: ['#EBF9EC', '#2EA121'], blue: ['#EFF4FF', '#245BDB'], warn: ['#FFF7E8', '#D97917'], red: ['#FDECEE', '#D83931'], gray: ['#F2F3F5', '#646A73'], purple: ['#F4EDFF', '#7F3BF5'] };
    const c = m[tone] || m.gray;
    return { display: 'inline-flex', alignItems: 'center', height: '22px', padding: '0 8px', borderRadius: '4px', background: c[0], color: c[1], fontSize: '12px', whiteSpace: 'nowrap' };
  }

  sw(on, accent) {
    return {
      knob: { position: 'absolute', top: '2px', left: on ? '18px' : '2px', width: '14px', height: '14px', borderRadius: '50%', background: '#fff', transition: 'left .16s ease', boxShadow: '0 1px 2px rgba(0,0,0,.15)' },
      track: { position: 'relative', display: 'inline-block', width: '34px', height: '18px', borderRadius: '9px', background: on ? accent : '#DEE0E3', cursor: 'pointer', flex: 'none' },
    };
  }


  db = {
    radarLinks: [
      { id: 1, title: 'AI 增长陪跑详情页 · 追踪版', tt: 'link', tl: '文章', src: 'xinliushangye.com/peipao?utm=radar', on: true, auth: true, pv: '1,284', uv: '912', view: '412', last: '08-05 09:41', code: 'a8f3k2', staff: 'HuangYouCan' },
      { id: 2, title: '共学营开营通知（可追踪）', tt: 'link', tl: 'H5', src: 'mp.weixin.qq.com/s/gongxueying-kaiying', on: true, auth: true, pv: '642', uv: '508', view: '188', last: '08-04 22:10', code: 'h5gx92', staff: 'HuangYouCan' },
      { id: 3, title: '课程试听页', tt: 'link', tl: '文章', src: 'xinliushangye.com/trial', on: false, auth: true, pv: '218', uv: '196', view: '54', last: '07-28 16:32', code: 'tri1x0', staff: 'WangWei' },
      { id: 4, title: '5天沙龙课程大纲.pdf', tt: 'pdf', tl: 'PDF', src: '5天沙龙课程大纲.pdf · 6.4 MB · 可预览 · 18 页', on: true, auth: true, pv: '396', uv: '301', view: '276', last: '08-05 08:15', code: 'pdf77a', staff: 'LinKaiYan' },
      { id: 5, title: '陪跑营学员案例长图', tt: 'image', tl: '图片', src: 'case-poster.png · 1.8 MB', on: true, auth: false, pv: '175', uv: '0', view: '163', last: '08-03 19:02', code: 'img55c', staff: 'ZhangMin' },
    ],
    radarEvents: [
      { u: 'oX9q****3kFm', e: 'wmXk3ABBAA_2f9Qn', t: '2026-08-05 09:41' },
      { u: 'oX9q****8tQw', e: 'wmXk3ABBAA_7hTzR', t: '2026-08-05 09:04' },
      { u: 'oX9q****c2Lp', e: 'wmXk3ABBAA_1k0pM', t: '2026-08-05 08:27' },
      { u: 'oX9q****77Hs', e: 'wmXk3ABBAA_9qWe2', t: '2026-08-04 23:52' },
      { u: 'oX9q****mK41', e: 'wmXk3ABBAA_4rTy8', t: '2026-08-04 22:10' },
      { u: 'oX9q****z09P', e: 'wmXk3ABBAA_6yUi3', t: '2026-08-04 21:38' },
      { u: 'oX9q****Qw8e', e: 'wmXk3ABBAA_3eWd7', t: '2026-08-04 19:55' },
      { u: 'oX9q****pL3d', e: 'wmXk3ABBAA_5tYu1', t: '2026-08-04 18:02' },
    ],
    aiPlans: [
      { id: 1, name: 'Agent 生成待发送计划 · wmbNXyCwAAVOJWev3kbMZkabwZuB2wmA', owner: 'HuangYouCan', updated: '2026/8/5 20:27:44', target: '1', status: '已批准', tone: 'ok' },
      { id: 2, name: 'Agent 生成待发送计划 · wmbNXyCwAAaKqerymAj9qOA7FjIxhFLQ', owner: 'HuangYouCan', updated: '2026/8/5 10:53:46', target: '1', status: '已批准', tone: 'ok' },
      { id: 3, name: 'Agent 生成待发送计划 · wmbNXyCwAAaKqerymAj9qOA7FjIxhFLQ', owner: 'ZhuQingHui', updated: '2026/8/5 09:18:20', target: '1', status: '已批准', tone: 'ok' },
      { id: 4, name: '2026-08-04 · HuangYouCan · ABCD v3 · 19:30 · AI助手审阅', owner: 'HuangYouCan', updated: '2026/8/4 18:00:51', target: '801', status: '已拒绝', tone: 'red' },
      { id: 5, name: '【已替代】2026-08-04 · HuangYouCan · ABCD v3 · 19:30 · AI助手审阅', owner: 'HuangYouCan', updated: '2026/8/4 17:56:04', target: '497', status: '已拒绝', tone: 'red' },
      { id: 6, name: '2026-08-03 · WangWei · 沉默唤回 · 15:00 · AI助手审阅', owner: 'WangWei', updated: '2026/8/3 15:57:41', target: '1', status: '待审批', tone: 'warn' },
      { id: 7, name: '2026-08-05 · LinKaiYan · 共学营开课提醒 · 09:00', owner: 'LinKaiYan', updated: '2026/8/5 08:42:10', target: '1,632', status: '待审批', tone: 'warn' },
    ],
    aiRcs: [
      { id: 1, name: '林晓', ext: 'wmXk3ABBAA_2f9Qn', owner: 'LinKaiYan', updated: '2026/8/5 08:42', count: '2', status: '待审阅', tone: 'warn', tasks: [
        { no: '话术任务 1 · 首轮触达', text: '{{客户名}} 你好，今晚 20:00 共学营正式开营，课程表和听课入口已发你，记得准时来～', media: '小程序卡片：课程详情页 · 图片：开营海报.png' },
        { no: '话术任务 2 · 跟进话术 · 第1次', text: '昨晚的开营直播回放已生成，抽空 10 分钟看一下重点部分，有问题随时问我。', media: '无素材，纯文本' },
      ] },
      { id: 2, name: '陈默', ext: 'wmXk3ABBAA_7hTzR', owner: 'LinKaiYan', updated: '2026/8/5 08:42', count: '1', status: '待审阅', tone: 'warn', tasks: [
        { no: '话术任务 1 · 首轮触达', text: '{{客户名}} 你好，今晚 20:00 共学营正式开营，课程表和听课入口已发你，记得准时来～', media: '小程序卡片：课程详情页' },
      ] },
      { id: 3, name: '刘靖', ext: 'wmXk3ABBAA_1k0pM', owner: 'LinKaiYan', updated: '2026/8/5 08:42', count: '3', status: '已批准', tone: 'ok', tasks: [
        { no: '话术任务 1 · 首轮触达', text: '{{客户名}} 你好，今晚 20:00 共学营正式开营…', media: '小程序卡片：课程详情页' },
        { no: '话术任务 2 · 跟进话术 · 第1次', text: '昨晚的开营直播回放已生成…', media: '无素材，纯文本' },
        { no: '话术任务 3 · 跟进话术 · 第2次', text: '明晚 19:30 答疑专场，提前把问题发我。', media: '无素材，纯文本' },
      ] },
      { id: 4, name: '王恺', ext: 'wmXk3ABBAA_9qWe2', owner: 'LinKaiYan', updated: '2026/8/5 08:42', count: '1', status: '待审阅', tone: 'warn', tasks: [
        { no: '话术任务 1 · 首轮触达', text: '{{客户名}} 你好，今晚 20:00 共学营正式开营…', media: '图片：开营海报.png' },
      ] },
      { id: 5, name: '张敏', ext: 'wmXk3ABBAA_4rTy8', owner: 'LinKaiYan', updated: '2026/8/5 08:42', count: '2', status: '已批准', tone: 'ok', tasks: [
        { no: '话术任务 1 · 首轮触达', text: '{{客户名}} 你好，今晚 20:00 共学营正式开营…', media: '小程序卡片：课程详情页' },
        { no: '话术任务 2 · 跟进话术 · 第1次', text: '昨晚的开营直播回放已生成…', media: '无素材，纯文本' },
      ] },
      { id: 6, name: '李由', ext: 'wmXk3ABBAA_6yUi3', owner: 'LinKaiYan', updated: '2026/8/5 08:42', count: '1', status: '已拒绝', tone: 'red', tasks: [
        { no: '话术任务 1 · 首轮触达', text: '{{客户名}} 你好，今晚 20:00 共学营正式开营…', media: '小程序卡片：课程详情页' },
      ] },
    ],
    funnelRows: [
      { m: '138****6072', n: '林晓', f: '已激活并打开', tone: 'ok', e: 'wmXk3ABBAA_2f9Qn', o: 'LinKaiYan', pool: true, qn: true, msg: '286', at: '2026-08-05', views: [0, 2] },
      { m: '139****2210', n: '陈默', f: '仅激活未打开', tone: 'warn', e: 'wmXk3ABBAA_7hTzR', o: 'ZhangMin', pool: true, qn: true, msg: '12', at: '2026-07-30', views: [0, 1] },
      { m: '131****8891', n: '刘靖', f: '未激活', tone: 'gray', e: '', o: '—', pool: true, qn: false, msg: '0', at: '', views: [0] },
      { m: '137****4410', n: '王恺', f: '注册但无会员', tone: 'red', e: 'wmXk3ABBAA_1k0pM', o: 'LiYou', pool: false, qn: true, msg: '45', at: '2026-08-04', views: [0] },
      { m: '135****9023', n: '张敏', f: '已激活并打开', tone: 'ok', e: 'wmXk3ABBAA_9qWe2', o: 'LinKaiYan', pool: true, qn: true, msg: '190', at: '2026-08-05', views: [0, 2] },
      { m: '186****3355', n: '李由', f: '仅激活未打开', tone: 'warn', e: 'wmXk3ABBAA_4rTy8', o: 'ZhangMin', pool: true, qn: false, msg: '8', at: '2026-07-22', views: [0, 1] },
      { m: '159****7741', n: '周航', f: '仅激活未打开', tone: 'warn', e: 'wmXk3ABBAA_6yUi3', o: 'HuangYouCan', pool: false, qn: true, msg: '21', at: '2026-08-01', views: [0, 1] },
      { m: '132****0087', n: '吴桐', f: '已激活并打开', tone: 'ok', e: 'wmXk3ABBAA_3eWd7', o: 'LiYou', pool: true, qn: true, msg: '342', at: '2026-08-05', views: [0, 2] },
    ],
  };

  notify(msg, err) {
    this.setState({ toast: { msg, err: !!err } });
    clearTimeout(this.__tt);
    this.__tt = setTimeout(() => this.setState({ toast: null }), 2400);
  }
  ask(title, body, okLabel, danger, onOk) {
    this.setState({ modal: { title, body, okLabel, danger, onOk } });
  }
  copyIt(text) {
    const done = () => this.notify('已复制到剪贴板');
    if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(text).then(done, done);
    else done();
  }
  startUpload() {
    if (this.state.upPct !== null) return;
    this.setState({ upPct: 0 });
    const tick = () => {
      const p = Math.min(100, (this.state.upPct ?? 0) + 14 + Math.random() * 10);
      this.setState({ upPct: p });
      if (p < 100) this.__up = setTimeout(tick, 160);
      else {
        setTimeout(() => {
          this.setState({ upPct: null, radarMedia: { name: this.state.radarType === 'image' ? '新上传图片.jpg' : '新上传文档.pdf', meta: this.state.radarType === 'image' ? 'image/jpeg · 2.1 MB · 刚上传' : 'application/pdf · 8.4 MB · 处理中' } });
          this.notify('上传完成');
        }, 320);
      }
    };
    this.__up = setTimeout(tick, 160);
  }

  renderVals() {
    const board = this.mode === 'board';
    const rail = this.rail;
    const accent = this.props.accent || '#3370ff';
    const rp = this.props.density === '舒适' ? '14px' : '10px';
    const page = this.state.page;

    const PARENT = { customers: 'customers', customerDetail: 'customers', questionnaires: 'questionnaires', questionnaireDetail: 'questionnaires', channels: 'channels', channelForm: 'channels', orders: 'orders', orderDetail: 'orders', spProducts: 'spProducts', coupons: 'coupons', couponForm: 'coupons', images: 'images', agents: 'agents', agentEdit: 'agents', config: 'config', configDetail: 'config', automation: 'automation', cycles: 'cycles', groupops: 'groupops', ai: 'ai', funnel: 'funnel', radar: 'radar', tags: 'tags', products: 'products', mpLib: 'mpLib', attach: 'attach', ownerMig: 'ownerMig', apidocs: 'apidocs', productForm: 'products', spProductForm: 'spProducts', groupopsDetail: 'groupops', radarDetail: 'radar', radarForm: 'radar', aiDetail: 'ai' };
    const SCREENS = Object.keys(PARENT);
    const NAV = ['automation', 'cycles', 'groupops', 'channels', 'ai', 'customers', 'funnel', 'questionnaires', 'radar', 'tags', 'orders', 'products', 'spProducts', 'coupons', 'images', 'mpLib', 'attach', 'agents', 'ownerMig', 'config', 'apidocs'];

    const f = {};
    SCREENS.forEach((k) => {
      f[k] = board
        ? { display: 'flex', flexDirection: 'column' }
        : { display: page === k ? 'flex' : 'none', flexDirection: 'column', flex: 1, minHeight: 0, minWidth: 0 };
    });

    const go = {};
    SCREENS.forEach((k) => { go[k] = () => this.setState({ page: k }); });

    const cstep = this.state.cstep;
    const cgo = {};
    const cn = {};
    const cp = {};
    [1, 2, 3, 4, 5].forEach((i) => {
      const on = cstep === i;
      cgo[i] = () => this.setState({ cstep: i });
      cn[i] = {
        display: 'flex', alignItems: 'center', gap: '10px', height: '44px', padding: '0 12px',
        borderRadius: '8px', cursor: 'pointer', fontSize: '14px',
        background: on ? '#EFF4FF' : 'transparent',
        color: on ? accent : '#1F2329',
        fontWeight: on ? 600 : 400,
      };
      cn['dot' + i] = {
        width: '22px', height: '22px', borderRadius: '50%', flex: 'none',
        display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: '12px',
        background: on ? accent : '#F2F3F5', color: on ? '#fff' : '#8F959E', fontWeight: 500,
      };
      cp[i] = { display: on ? 'block' : 'none' };
    });

    const astep = this.state.astep || 1;
    const ago = {};
    const an = {};
    const ap = {};
    [1, 2, 3, 4].forEach((i) => {
      const on = astep === i;
      ago[i] = () => this.setState({ astep: i });
      an[i] = {
        display: 'flex', alignItems: 'center', gap: '10px', height: '44px', padding: '0 12px',
        borderRadius: '8px', cursor: 'pointer', fontSize: '14px',
        background: on ? '#EFF4FF' : 'transparent', color: on ? accent : '#1F2329', fontWeight: on ? 600 : 400,
      };
      an['dot' + i] = {
        width: '22px', height: '22px', borderRadius: '50%', flex: 'none', display: 'flex',
        alignItems: 'center', justifyContent: 'center', fontSize: '12px',
        background: on ? accent : '#F2F3F5', color: on ? '#fff' : '#8F959E', fontWeight: 500,
      };
      ap[i] = { display: on ? 'block' : 'none' };
    });

    const activeNav = PARENT[page];
    const n = {};
    NAV.forEach((k) => {
      const on = k === activeNav;
      n[k] = {
        display: 'flex', alignItems: 'center', gap: '10px', height: '34px',
        padding: rail ? '0 12px' : '0 10px', margin: '0 8px 2px', borderRadius: '6px',
        fontSize: '13px', cursor: 'pointer', whiteSpace: 'nowrap', overflow: 'hidden',
        background: on ? '#EFF4FF' : 'transparent',
        color: on ? accent : '#1F2329',
        fontWeight: on ? 600 : 400,
      };
    });

    const seg = (on) => ({ height: '26px', padding: '0 14px', border: 0, borderRadius: '5px', background: on ? '#fff' : 'transparent', color: on ? '#1F2329' : '#C4C7CC', fontSize: '12px', fontWeight: on ? 600 : 400, cursor: 'pointer' });

    const mk = (tone) => this.chip(tone);

    return {
      vars: { display: 'flex', flexDirection: 'column', height: '100%', minHeight: '100vh', '--accent': accent, '--rp': rp },
      setProto: () => this.setState({ mode: 'proto' }),
      setBoard: () => this.setState({ mode: 'board' }),
      toggleRail: () => this.setState({ rail: !rail }),
      tabProto: seg(!board),
      tabBoard: seg(board),
      railBtn: { whiteSpace: 'nowrap', flex: 'none', height: '26px', padding: '0 10px', borderRadius: '5px', border: '1px solid #3E434A', background: 'transparent', color: '#C4C7CC', fontSize: '12px', cursor: 'pointer', display: board ? 'none' : 'inline-flex', alignItems: 'center' },
      railLabel: rail ? '展开侧栏' : '收起侧栏',

      sidebar: board
        ? { display: 'none' }
        : { width: rail ? '56px' : '208px', flex: 'none', display: 'flex', flexDirection: 'column', background: '#fff', borderRight: '1px solid #DEE0E3', transition: 'width .18s ease' },
      lbl: { display: rail ? 'none' : 'block', minWidth: 0 },
      grp: { display: rail ? 'none' : 'block', padding: '14px 18px 5px', fontSize: '12px', color: '#A6AAB0' },
      userBox: { display: 'flex', alignItems: 'center', gap: '10px', padding: rail ? '10px 14px' : '10px 16px', borderTop: '1px solid #EFF0F1', flex: 'none' },
      n, go, f, cgo, cn, cp,
      promptTokens: ['插入 {{问卷信息}}', '插入 {{最近20条聊天信息}}', '插入 {{用户标签}}', '插入 {{激活信息}}'].map((t) => ({ t })),
      welcomePreview: '{{客户名}} 恭喜你报名成功#5天沙龙邀约破局共学营\n⭕辛苦填一下问卷，让我们更好了解你的需求，给你提供更好的服务：\nhttps://www.xinliushangye.com/s/salon-yixiang-gongxueying\n\n⏰开营时间：8月6日\n🎯进群时间：8月5日',
      taskPrompt: '问卷信息：\n{{问卷信息}}\n\n从下面 12 门课程中推荐 2 门：',
      welcomeText: '{{客户名}} 恭喜你报名成功#5天沙龙邀约破局共学营\n⭕辛苦填一下问卷，让我们更好了解你的需求，给你提供更好的服务：\nhttps://www.xinliushangye.com/s/salon-yixiang-gongxueying\n\n⏰开营时间：8月6日\n🎯进群时间：8月5日',
      stepTitle: ['基础配置', '渠道载体', '客服分配', '欢迎语素材', '入渠标签'][cstep - 1],
      ago, an, ap,
      aTitle: ['基本信息', '当前绑定人群包', 'Prompt 配置', '固定素材'][astep - 1],

      // ===== 全局反馈（toast / 确认浮窗） =====
      toastShow: !!this.state.toast,
      toastMsg: (this.state.toast || {}).msg || '',
      toastStyle: { position: 'fixed', right: '22px', bottom: '22px', zIndex: 999, background: (this.state.toast || {}).err ? '#D83931' : '#1F2329', color: '#fff', padding: '10px 16px', borderRadius: '8px', fontSize: '13px', boxShadow: '0 12px 28px rgba(15,23,42,.25)' },
      modalOpen: !!this.state.modal,
      modalTitle: (this.state.modal || {}).title || '',
      modalLines: ((this.state.modal || {}).body || []).map((t) => ({ t })),
      modalOkLabel: (this.state.modal || {}).okLabel || '确认',
      modalOkStyle: (this.state.modal || {}).danger
        ? { height: '32px', padding: '0 14px', borderRadius: '6px', border: '1px solid #F2B8B5', background: '#fff', color: '#D83931', fontSize: '13px', cursor: 'pointer' }
        : { height: '32px', padding: '0 14px', borderRadius: '6px', border: 0, background: accent, color: '#fff', fontSize: '13px', cursor: 'pointer' },
      modalOk: () => { const m = this.state.modal; this.setState({ modal: null }); if (m && m.onOk) m.onOk(); },
      modalClose: () => this.setState({ modal: null }),

      // ===== 内容雷达 =====
      radarRows: this.db.radarLinks.map((r) => ({
        ...r,
        typeCs: mk(r.tt === 'link' ? 'blue' : r.tt === 'image' ? 'ok' : 'red'),
        stCs: mk(r.on ? 'ok' : 'gray'),
        stLabel: r.on ? '启用' : '停用',
        toggleLabel: r.on ? '停用' : '启用',
        rowBg: r.on ? {} : { background: '#FAFAFB' },
        openDetail: () => this.setState({ radarCur: r.id, page: 'radarDetail' }),
        openEdit: () => this.setState({ radarCur: r.id, radarType: r.tt, radarMedia: r.tt === 'link' ? null : { name: r.src.split(' · ')[0], meta: r.src.split(' · ').slice(1).join(' · ') }, page: 'radarForm' }),
        share: () => this.setState({ modal: { title: '分享内容雷达', body: ['雷达链接：https://crm.example.com/r/' + r.code, '二维码已生成，可在正式环境中下载。'], okLabel: '复制链接', danger: false, onOk: () => this.copyIt('https://crm.example.com/r/' + r.code) } }),
        toggle: () => this.ask(r.on ? '确认停用' : '确认启用', ['「' + r.title + '」' + (r.on ? '停用后访问者将看到已失效提示。' : '启用后链接立即恢复访问。')], r.on ? '确认停用' : '确认启用', r.on, () => { r.on = !r.on; this.notify(r.on ? '已启用' : '已停用'); }),
      })),
      rdCur: (() => { const r = this.db.radarLinks.find((x) => x.id === this.state.radarCur) || this.db.radarLinks[0]; return { ...r, typeCs: mk(r.tt === 'link' ? 'blue' : r.tt === 'image' ? 'ok' : 'red'), stCs: mk(r.on ? 'ok' : 'gray'), stLabel: r.on ? '启用' : '停用', authLabel: r.auth ? '是' : '否' }; })(),
      radarEvents: this.db.radarEvents,
      rdEdit: () => { const r = this.db.radarLinks.find((x) => x.id === this.state.radarCur); this.setState({ radarType: r.tt, radarMedia: r.tt === 'link' ? null : { name: r.src.split(' · ')[0], meta: r.src.split(' · ').slice(1).join(' · ') }, page: 'radarForm' }); },
      rdCopy: () => { const r = this.db.radarLinks.find((x) => x.id === this.state.radarCur); this.copyIt('https://crm.example.com/r/' + r.code); },
      rdExport: () => this.notify('导出任务已创建，完成后自动下载'),
      rdRefresh: () => this.notify('已刷新'),
      rfCards: [
        { t: 'link', b: '外部链接', d: '跳转到任意 http/https 页面，到达即记录', help: '仅允许合法 http/https 外部链接，禁止 localhost、内网 IP 和脚本协议。' },
        { t: 'image', b: '图片预览', d: '授权后云端预览单图，≤ 10MB', help: '可从图片素材库选择，或上传 JPG/PNG/WEBP。' },
        { t: 'pdf', b: 'PDF 预览', d: '授权后在线翻页预览，≤ 50MB', help: '可从附件素材库选择，超过 1MB 自动分片上传。' },
      ].map((c) => ({
        ...c,
        cs: { border: '1px solid ' + (this.state.radarType === c.t ? accent : '#DEE0E3'), borderRadius: '8px', padding: '12px', cursor: 'pointer', display: 'grid', gap: '6px', background: this.state.radarType === c.t ? '#EFF4FF' : '#fff' },
        pick: () => this.setState({ radarType: c.t, radarMedia: null }),
      })),
      rfIsLink: this.state.radarType === 'link',
      rfIsMedia: this.state.radarType !== 'link',
      rfTypeHelp: (this.state.radarType === 'image' ? '可从图片素材库选择，或上传 JPG/PNG/WEBP，最大 10MB。' : '可从附件素材库选择 PDF，或上传 PDF，最大 50MB，超过 1MB 自动分片上传。'),
      rfMediaLabel: this.state.radarType === 'image' ? '图片素材 *' : 'PDF 素材 *',
      rfMedia: this.state.radarMedia,
      rfPick: () => this.setState({ radarMedia: { name: this.state.radarType === 'image' ? '沙龙主视觉-终版.png' : '5天沙龙课程大纲.pdf', meta: this.state.radarType === 'image' ? 'image/png · 1.8 MB · media_item_id: img_3382' : 'application/pdf · 6.4 MB · 可预览 · 18 页' } }),
      rfRemoveMedia: () => this.setState({ radarMedia: null }),
      rfUpload: () => this.startUpload(),
      upShow: this.state.upPct !== null,
      upBar: { height: '100%', width: (this.state.upPct || 0) + '%', background: accent, borderRadius: '99px', transition: 'width .15s linear' },
      upPctText: Math.floor(this.state.upPct || 0) + '%',
      rfSaveLabel: this.state.saving ? '⏳ 保存中…' : '保存内容雷达',
      rfSave: () => {
        if (this.state.saving) return;
        if (this.state.radarType !== 'link' && !this.state.radarMedia) { this.notify('请选择或上传素材', true); return; }
        this.setState({ saving: true });
        setTimeout(() => { this.setState({ saving: false, page: 'radar' }); this.notify('已保存内容雷达'); }, 700);
      },
      rfEnabled: this.state.radarEnabled !== false,
      rfAuth: this.state.radarAuth !== false,
      rfSwE: () => this.setState({ radarEnabled: this.state.radarEnabled === false }),
      rfSwA: () => this.setState({ radarAuth: this.state.radarAuth === false }),

      // ===== AI 助手 =====
      aiPlans: this.db.aiPlans.map((p) => ({
        ...p,
        stCs: mk(p.tone),
        open: () => this.setState({ aiCur: p.id, aiShown: 3, page: 'aiDetail' }),
      })),
      aiCurPlan: (() => { const p = this.db.aiPlans.find((x) => x.id === this.state.aiCur) || this.db.aiPlans[0]; return { ...p, stCs: mk(p.tone) }; })(),
      aiRcs: this.db.aiRcs.slice(0, this.state.aiShown).map((r) => ({
        ...r,
        stCs: mk(r.tone),
        open: () => this.setState({ rcDrawer: r.id }),
      })),
      aiLoadedText: '已加载 ' + Math.min(this.state.aiShown, this.db.aiRcs.length) + ' / ' + this.db.aiRcs.length + ' 人（演示数据截取）',
      aiBar: { display: 'block', height: '100%', width: (Math.min(this.state.aiShown, this.db.aiRcs.length) / this.db.aiRcs.length * 100) + '%', background: '#2EA121', borderRadius: '99px' },
      aiLoadMore: () => this.setState({ aiShown: this.state.aiShown + 50 }),
      aiApprove: () => {
        const p = this.db.aiPlans.find((x) => x.id === this.state.aiCur);
        if (p.status !== '待审批') return;
        this.ask('确认并发送', ['计划「' + p.name + '」将派发给 ' + p.target + ' 名目标人员。', '发送人：' + p.owner + ' · 确认后立即生效。'], '确认并发送', false, () => {
          p.status = '已批准'; p.tone = 'ok';
          this.notify('计划已批准，等待派发');
        });
      },
      aiReject: () => {
        const p = this.db.aiPlans.find((x) => x.id === this.state.aiCur);
        if (p.status !== '待审批') return;
        this.ask('拒绝计划', ['计划「' + p.name + '」将被拒绝，不会发送任何消息。'], '确认拒绝', true, () => {
          p.status = '已拒绝'; p.tone = 'red';
          this.notify('计划已拒绝');
        });
      },
      rcOpen: this.state.rcDrawer !== null,
      rcCur: (() => { const r = this.db.aiRcs.find((x) => x.id === this.state.rcDrawer) || null; return r ? { ...r, stCs: mk(r.tone) } : null; })(),
      dwClose: () => this.setState({ rcDrawer: null }),
      dwApprove: () => {
        const r = this.db.aiRcs.find((x) => x.id === this.state.rcDrawer);
        if (!r || r.status !== '待审阅') return;
        r.status = '已批准'; r.tone = 'ok';
        this.notify('已批准这个人发送');
      },
      dwReject: () => {
        const r = this.db.aiRcs.find((x) => x.id === this.state.rcDrawer);
        if (!r || r.status !== '待审阅') return;
        r.status = '已拒绝'; r.tone = 'red';
        this.notify('已拒绝这个人');
      },

      // ===== 漏斗 · 多维表格 =====
      fTabs: ['全部人群', '拉回重点 · 仅激活未打开', '高价值会员'].map((t, i) => ({
        t,
        cs: { display: 'flex', alignItems: 'center', height: '40px', padding: '0 14px', fontSize: '13px', cursor: 'pointer', whiteSpace: 'nowrap', borderBottom: '2px solid ' + (this.state.fView === i ? accent : 'transparent'), color: this.state.fView === i ? '#245BDB' : '#646A73', fontWeight: this.state.fView === i ? 600 : 400 },
        open: () => this.setState({ fView: i }),
      })),
      fShare: () => this.setState({ modal: { title: '分享数据', body: ['邀请协作者：从已同步的企微员工目录邀请，可设置可查看 / 可编辑。', '外部分享：开启后获得链接的人可免登录查看全部字段和保存的视图。'], okLabel: '复制外部分享链接', danger: false, onOk: () => this.copyIt('https://crm.example.com/share/funnel/v_demo') } }),
      fAddView: () => this.notify('演示环境：正式环境将打开视图编辑器'),
      fPanelOpen: this.state.fPanel !== null,
      fPanelTitle: { filter: '筛选：满足以下所有条件的行才会显示', group: '分组：按字段分组，组可折叠', sort: '排序规则' }[this.state.fPanel] || '',
      fPanelLines: {
        filter: ['漏斗状态 等于 「' + (this.state.fView === 1 ? '仅激活未打开' : this.state.fView === 2 ? '已激活并打开' : '全部') + '」', '＋ 添加条件（文本 / 枚举 / 布尔 / 数字区间 / 日期区间）'],
        group: ['按「企微跟进人」分组，组头显示行数，可折叠', '支持任意枚举字段：班期标签 / 入口来源 / 会员类型…'],
        sort: ['按「用户消息数」从高到低', '点表头可快速切换排序方向'],
      }[this.state.fPanel] || [],
      fpBtnFilter: { height: '28px', padding: '0 12px', borderRadius: '6px', border: '1px solid ' + (this.state.fPanel === 'filter' ? accent : '#DEE0E3'), background: this.state.fPanel === 'filter' ? '#EFF4FF' : '#fff', color: this.state.fPanel === 'filter' ? '#245BDB' : '#1F2329', fontSize: '12px', cursor: 'pointer' },
      fpBtnGroup: { height: '28px', padding: '0 12px', borderRadius: '6px', border: '1px solid ' + (this.state.fPanel === 'group' ? accent : '#DEE0E3'), background: this.state.fPanel === 'group' ? '#EFF4FF' : '#fff', color: this.state.fPanel === 'group' ? '#245BDB' : '#1F2329', fontSize: '12px', cursor: 'pointer' },
      fpBtnSort: { height: '28px', padding: '0 12px', borderRadius: '6px', border: '1px solid ' + (this.state.fPanel === 'sort' ? accent : '#DEE0E3'), background: this.state.fPanel === 'sort' ? '#EFF4FF' : '#fff', color: this.state.fPanel === 'sort' ? '#245BDB' : '#1F2329', fontSize: '12px', cursor: 'pointer' },
      fpFilter: () => this.setState({ fPanel: this.state.fPanel === 'filter' ? null : 'filter' }),
      fpGroup: () => this.setState({ fPanel: this.state.fPanel === 'group' ? null : 'group' }),
      fpSort: () => this.setState({ fPanel: this.state.fPanel === 'sort' ? null : 'sort' }),
      fSummary: ['共 18,204 行 · 未分组', '共 1,286 行 · 已按「企微跟进人」分组', '共 6,412 行 · 已按「会员类型」分组'][this.state.fView],
      funnelRows: this.db.funnelRows.filter((r) => r.views.includes(this.state.fView)).map((r) => ({
        ...r,
        fCs: mk(r.tone),
        poolTick: r.pool ? '✓' : '✗',
        qnTick: r.qn ? '✓' : '✗',
      })),
      fRefresh: () => this.notify('刷新成功'),


      stage: board
        ? { flex: 1, minWidth: 0, overflow: 'auto', display: 'grid', gridTemplateColumns: 'repeat(3, 686px)', gap: '30px 26px', padding: '26px', alignContent: 'start', justifyContent: 'start', background: '#EBEDF0' }
        : { flex: 1, minWidth: 0, minHeight: 0, display: 'flex', background: '#F5F6F7' },
      cap: board
        ? { display: 'block', margin: '0 0 8px', fontSize: '12px', fontWeight: 600, color: '#646A73' }
        : { display: 'none' },
      box: board
        ? { width: '686px', height: '452px', overflow: 'hidden', borderRadius: '10px', border: '1px solid #DEE0E3', background: '#fff', boxShadow: '0 2px 12px rgba(31,35,41,.07)' }
        : { flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' },
      scale: board
        ? { width: '1240px', height: '818px', transform: 'scale(0.553)', transformOrigin: 'top left', display: 'flex', flexDirection: 'column', background: '#F5F6F7' }
        : { flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column', background: '#F5F6F7' },

      rows: {
        customers: [
          { name: '李思远', id: 'wmA8c3DQAA_x91', owner: '张敏', mobile: '138****4021' },
          { name: '陈曦', id: 'wmA8c3DQAA_k27', owner: '张敏', mobile: '159****7783' },
          { name: '周雨桐', id: 'wmA8c3DQAA_p04', owner: '李由', mobile: '186****2210' },
          { name: '王一鸣', id: 'wmA8c3DQAA_b58', owner: '未分配', mobile: '未填写' },
          { name: '赵启明', id: 'wmA8c3DQAA_h33', owner: '李由', mobile: '137****9046' },
          { name: '孙婉宁', id: 'wmA8c3DQAA_c12', owner: '王恺', mobile: '135****6672' },
        ],
        tags: [{ name: '高意向' }, { name: '已看直播' }, { name: '课程A-试听' }, { name: '南方区域' }, { name: '价格敏感' }, { name: '待跟进' }],
        qa: [
          { q: '你目前最想解决的问题是？', a: '获客成本太高' },
          { q: '团队规模', a: '10 – 30 人' },
          { q: '每月投放预算', a: '3 万以内' },
          { q: '手机号', a: '138****4021' },
        ],
        msgs: [
          { who: '客户', time: '08-01 10:22', text: '这个周期服务能开发票吗？', me: false },
          { who: '张敏', time: '08-01 10:24', text: '可以的，付款后在订单页申请，1 个工作日开具。', me: true },
          { who: '客户', time: '08-02 09:10', text: '好的，我先报名试试。', me: false },
          { who: '张敏', time: '08-02 09:12', text: '已经把报名链接发你，有问题随时找我。', me: true },
        ].map((m) => ({
          ...m,
          wrap: { alignSelf: m.me ? 'flex-end' : 'flex-start', maxWidth: '86%', textAlign: m.me ? 'right' : 'left' },
          bubble: {
            display: 'inline-block', padding: '8px 12px', borderRadius: m.me ? '10px 10px 2px 10px' : '10px 10px 10px 2px',
            background: m.me ? accent : '#F2F3F5', color: m.me ? '#fff' : '#1F2329', fontSize: '13px', lineHeight: '20px', textAlign: 'left',
          },
        })),
        qStats: [
          { label: '问卷总数', value: '18', unit: '份' },
          { label: '已发布', value: '11', unit: '份' },
          { label: '本周提交', value: '1,342', unit: '次' },
          { label: '外推失败', value: '3', unit: '条' },
        ],
        questionnaires: [
          { name: '增长诊断测评', assess: true, off: false, action: '跳转 H5 页面', created: '2026-05-12 10:04', count: '2318' },
          { name: '课程满意度回访', assess: false, off: false, action: '打开小程序 URL Link', created: '2026-04-28 16:31', count: '806' },
          { name: '私域用户画像', assess: true, off: false, action: '展示渠道二维码', created: '2026-04-02 09:12', count: '1455' },
          { name: '直播报名信息收集', assess: false, off: false, action: '打开微信小程序', created: '2026-03-19 14:47', count: '3902' },
          { name: '内容偏好调研', assess: false, off: false, action: '沿用原跳转', created: '2026-02-11 08:55', count: '624' },
          { name: '老客户续费意向', assess: false, off: true, action: '沿用原跳转', created: '2026-03-05 11:26', count: '0' },
        ].map((r) => ({
          ...r,
          status: r.off ? '已停用' : '启用中',
          cs: mk(r.off ? 'red' : 'ok'),
          toggle: r.off ? '启用' : '停用',
          rowStyle: r.off ? { background: '#FAFAFB' } : {},
          nameStyle: { fontSize: '13px', fontWeight: 600, color: r.off ? '#A6AAB0' : '#1F2329' },
          delStyle: { fontSize: '13px', cursor: r.off ? 'pointer' : 'not-allowed', color: r.off ? '#D83931' : '#BBBFC4' },
        })),

        qSubs: [
          { time: '2026-08-03 09:41', uid: 'wmA8c3DQAA_x91', by: 'unionid', score: '82', tags: ['高意向', '课程A'] },
          { time: '2026-08-03 09:12', uid: 'wmA8c3DQAA_k27', by: 'external_userid', score: '64', tags: ['待跟进'] },
          { time: '2026-08-02 21:37', uid: 'wmA8c3DQAA_p04', by: 'mobile', score: '91', tags: ['高意向', '南方区域'] },
          { time: '2026-08-02 18:05', uid: '-', by: '未匹配', score: '48', tags: [] },
        ],
        qApply: [
          { time: '2026-08-03 09:41', sid: '#20481', uid: 'wmA8c3DQAA_x91', status: '已完成', tone: 'ok', err: '-' },
          { time: '2026-08-03 09:12', sid: '#20480', uid: 'wmA8c3DQAA_k27', status: '已完成', tone: 'ok', err: '-' },
          { time: '2026-08-02 18:05', sid: '#20478', uid: '-', status: '失败', tone: 'red', err: '外部推送超时（504）' },
        ].map((r) => ({ ...r, cs: mk(r.tone) })),

        edTools: [
          { m: '单', t: '单选题', d: '只能选一个。' },
          { m: '多', t: '多选题', d: '可以选多个。' },
          { m: '文', t: '文本题', d: '开放回答。' },
          { m: '号', t: '手机号题', d: '收集联系方式。' },
          { m: '测', t: '添加多维测评模板', d: '从已创建模板中整组添加。' },
          { m: '规', t: '分数规则', d: '按总分区间打标签。' },
        ],
        edQs: [
          { tag: '文本题 · 必填', title: '你的微信昵称', ph: '多行文本输入', input: true, opts: [] },
          { tag: '手机号题 · 必填', title: '你的手机号', ph: '请输入手机号', input: true, opts: [] },
          { tag: '单选题 · 必填', title: '你目前所处的行业是?', ph: '', input: false, opts: ['美容 / 美发 / 美甲', '健康 / 养生 / 大健康', '教育培训 / 知识付费', '服装', '保险', '心理、疗愈', '零售 / 电商（含社交电商）', '高端餐饮', '珠宝', '形体礼仪', '空间、小院', '瑜伽、产康、普拉提', '其他'] },
        ].map((q) => ({ ...q, isOpts: !q.input })),
        edAssignees: [{ name: '林小楷', uid: 'LinKaiYan', ratio: '100' }],

        chStats: [
          { label: '渠道总数', value: '14', unit: '独立获客资源' },
          { label: '渠道资产', value: '14', unit: '二维码 / 链接资源' },
          { label: '企微获客助手链接', value: '0', unit: '复制 / 分享链接' },
          { label: '渠道用户', value: '687', unit: '渠道进入记录' },
        ],
        channels: [
          { name: '扫码添加，邀请你进群共学', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '0', qr: '下载二维码' },
          { name: '城市会员咨询', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '0', qr: '下载二维码' },
          { name: '城市会员咨询', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '0', qr: '生成二维码' },
          { name: '视频号8.8发布会', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '1', qr: '下载二维码' },
          { name: '加我企业微信，兑换课程', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '19', qr: '下载二维码' },
          { name: '浅蓝测试渠道码', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '无标签', tagTone: 'gray', users: '1', qr: '下载二维码' },
          { name: '有赞店铺来源', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '8', qr: '下载二维码' },
          { name: '城市会员 3980', type: '普通二维码', status: '启用', tone: 'ok', mat: '0 素材', tag: '标签', tagTone: 'ok', users: '41', qr: '下载二维码' },
        ].map((r) => ({ ...r, cs: mk(r.tone), tcs: mk(r.tagTone), typeCs: mk('blue'), matCs: mk('gray'), welCs: mk('ok') })),

        orders: [
          { time: '2026-08-03 09:52', no: '4200002419202608', plat: 'PAY20260803A19', payer: '李思远', uid: 'wmA8c3DQAA_x91', product: '增长陪跑 · 90 天', amount: '¥2,980.00', status: '已支付', tone: 'ok', pay: '微信支付' },
          { time: '2026-08-03 08:14', no: '4200002419188473', plat: 'PAY20260803A02', payer: '陈曦', uid: 'wmA8c3DQAA_k27', product: '内容诊断单次', amount: '¥399.00', status: '已支付', tone: 'ok', pay: '微信支付' },
          { time: '2026-08-02 22:41', no: '4200002418902117', plat: 'PAY20260802B77', payer: '周雨桐', uid: 'wmA8c3DQAA_p04', product: '增长陪跑 · 30 天', amount: '¥1,280.00', status: '退款中', tone: 'warn', pay: '微信支付' },
          { time: '2026-08-02 20:03', no: '-', plat: 'PAY20260802B54', payer: '王一鸣', uid: '-', product: '增长陪跑 · 90 天', amount: '¥2,980.00', status: '未支付', tone: 'gray', pay: '微信支付' },
          { time: '2026-08-02 15:26', no: '4200002418655902', plat: 'PAY20260802A31', payer: '赵启明', uid: 'wmA8c3DQAA_h33', product: '私域陪跑年卡', amount: '¥9,800.00', status: '已退款', tone: 'red', pay: '支付宝' },
        ].map((r) => ({ ...r, cs: mk(r.tone) })),

        orderKv: [
          { k: '微信单号', v: '4200002419202608', mono: true },
          { k: '商户单号', v: 'PAY20260803A19', mono: true },
          { k: '商品', v: '增长陪跑 · 90 天', mono: false },
          { k: '订单创建时间', v: '2026-08-03 09:52:14', mono: true },
          { k: '支付时间', v: '2026-08-03 09:52:41', mono: true },
          { k: '付款人', v: '李思远', mono: false },
          { k: '手机号', v: '138****4021', mono: true },
          { k: '客户身份', v: 'wmA8c3DQAA_x91', mono: true },
          { k: '金额', v: '¥2,980.00 CNY', mono: false },
          { k: '原始状态', v: 'SUCCESS / TRADE_SUCCESS', mono: true },
          { k: '已退款', v: '¥0.00', mono: false },
          { k: '退款处理中', v: '¥0.00', mono: false },
          { k: '可退款', v: '¥2,980.00', mono: false },
        ].map((r) => ({ ...r, vs: { fontSize: '13px', color: '#1F2329', fontFamily: r.mono ? 'ui-monospace,SFMono-Regular,Menlo,monospace' : 'inherit' } })),
        orderEvents: [
          { time: '2026-08-03 09:52:14', ev: '创建订单', st: '成功', tone: 'ok' },
          { time: '2026-08-03 09:52:38', ev: '拉起支付', st: '成功', tone: 'ok' },
          { time: '2026-08-03 09:52:41', ev: '支付回调 payment.success', st: '成功', tone: 'ok' },
          { time: '2026-08-03 09:52:42', ev: '开通周期服务', st: '成功', tone: 'ok' },
          { time: '2026-08-03 09:52:43', ev: '外部推送 order.paid', st: '重试 1 次后成功', tone: 'warn' },
        ].map((r) => ({ ...r, cs: mk(r.tone) })),

        spProducts: [
          { code: 'SP-GROW-90', name: '增长陪跑 · 90 天', price: '¥2,980.00', status: '已上架', tone: 'ok', sold: '412', updated: '2026-08-01 17:20' },
          { code: 'SP-GROW-30', name: '增长陪跑 · 30 天', price: '¥1,280.00', status: '已上架', tone: 'ok', sold: '1,036', updated: '2026-07-28 10:02' },
          { code: 'SP-VIP-365', name: '私域陪跑年卡', price: '¥9,800.00', status: '已上架', tone: 'ok', sold: '87', updated: '2026-07-19 09:41' },
          { code: 'SP-TRIAL-7', name: '体验期 7 天', price: '¥99.00', status: '未上架', tone: 'gray', sold: '2,204', updated: '2026-06-30 15:58' },
        ].map((r) => ({ ...r, cs: mk(r.tone) })),

        coupons: [
          { name: '新客立减 100', off: '¥100', scope: '增长陪跑 · 90 天 等 2 个', window: '08-01 00:00 – 08-31 23:59', issue: '1,200 / 486', status: '进行中', tone: 'ok' },
          { name: '老客续费 300', off: '¥300', scope: '私域陪跑年卡', window: '07-15 00:00 – 08-15 23:59', issue: '300 / 212', status: '进行中', tone: 'ok' },
          { name: '直播专享 50', off: '¥50', scope: '全部周期商品', window: '08-05 20:00 – 08-05 23:00', issue: '2,000 / 0', status: '未开始', tone: 'blue' },
          { name: '618 回馈券', off: '¥200', scope: '增长陪跑 · 30 天', window: '06-01 00:00 – 06-20 23:59', issue: '800 / 741', status: '已结束', tone: 'gray' },
          { name: '内测答谢券', off: '¥150', scope: '体验期 7 天', window: '05-01 00:00 – 05-31 23:59', issue: '100 / 12', status: '已停用', tone: 'red' },
        ].map((r) => ({ ...r, cs: mk(r.tone) })),

        images: [
          { name: '直播预告主视觉.png', size: '1080×1920 · 482 KB', tag: '直播', tone: 'blue', bg: 'linear-gradient(135deg,#DCE7FF,#B9CDFF)' },
          { name: '课程卡片-增长.jpg', size: '750×1000 · 213 KB', tag: '课程', tone: 'purple', bg: 'linear-gradient(135deg,#EADCFF,#CFB6FF)' },
          { name: '欢迎语配图.png', size: '900×600 · 156 KB', tag: '欢迎语', tone: 'ok', bg: 'linear-gradient(135deg,#D8F5DE,#AEE7BD)' },
          { name: '优惠券横幅.png', size: '1200×400 · 98 KB', tag: '活动', tone: 'warn', bg: 'linear-gradient(135deg,#FFE9CC,#FFD09B)' },
          { name: '案例长图-01.jpg', size: '750×4200 · 1.2 MB', tag: '案例', tone: 'gray', bg: 'linear-gradient(135deg,#E9EBEF,#CFD3DA)' },
          { name: '朋友圈九宫格-3.jpg', size: '1080×1080 · 340 KB', tag: '朋友圈', tone: 'blue', bg: 'linear-gradient(135deg,#DCE7FF,#AFC6FF)' },
          { name: '答疑海报.png', size: '1080×1440 · 512 KB', tag: '社群', tone: 'purple', bg: 'linear-gradient(135deg,#F0E2FF,#D6BBFF)' },
          { name: '门店物料码.png', size: '800×800 · 74 KB', tag: '渠道', tone: 'ok', bg: 'linear-gradient(135deg,#DAF3EC,#ADE0D2)' },
        ].map((r) => ({ ...r, cs: mk(r.tone), thumb: { height: '104px', background: r.bg, borderBottom: '1px solid #EFF0F1' } })),

        agents: [
          { name: '沙龙问卷 AI 跟进话术', code: 'salon_questionnaire_followup_agent', type: 'Agent 机器人', material: '0 图片 / 0 小程序 / 0 PDF / 0 群邀请', status: '启用中', tone: 'ok' },
          { name: '新建 Agent', code: 'new_agent_1784189809857', type: 'Agent 机器人', material: '0 图片 / 0 小程序 / 0 PDF / 0 群邀请', status: '启用中', tone: 'ok' },
          { name: '沙龙进群固定欢迎', code: 'salon_group_welcome_fixed', type: '固定话术', material: '1 图片 / 0 小程序 / 0 PDF / 1 群邀请', status: '已停止', tone: 'gray' },
        ].map((r) => ({ ...r, cs: mk(r.tone), typeCs: mk('gray'), matCs: mk('gray') })),
        agentSlots: [
          { label: '插入 {{问卷信息}}' }, { label: '插入 {{最近20条聊天信息}} ' }, { label: '插入 {{用户标签}}' }, { label: '插入 {{激活信息}}' },
        ],
        agentDeps: [{ t: '问卷信息' }, { t: '最近20条聊天' }, { t: '用户标签' }, { t: '激活信息' }],

        configCats: [
          { label: '企业微信', on: true, status: '已生效' },
          { label: '微信支付', on: true, status: '已生效' },
          { label: '公众号 OAuth', on: true, status: '已生效' },
          { label: '支付宝', on: false, status: '未生效' },
          { label: '对象存储 / CDN', on: true, status: '已生效' },
          { label: 'CRM 开放 API Key', on: true, status: '已生效' },
          { label: 'MCP 工具', on: false, status: '未生效' },
        ].map((r) => ({ ...r, cs: mk(r.on ? 'ok' : 'gray'), sw: this.sw(r.on, accent) })),

        configFields: [
          { label: '企业 ID（CorpID）', value: 'ww8f2c1d0a9b7e4c', kind: 'text' },
          { label: '应用 Secret', value: '', kind: 'secret', ph: '已设置' },
          { label: '通讯录 Secret', value: '', kind: 'secret', ph: '未设置' },
          { label: '回调 Token', value: 'Kq9XmZ2sVb', kind: 'text' },
          { label: '消息加解密 Key', value: '', kind: 'secret', ph: '已设置' },
          { label: '开启外部联系人同步', value: '', kind: 'switch', on: true },
          { label: '同步间隔（分钟）', value: '30', kind: 'number' },
          { label: '回调地址', value: 'https://crm.example.com/api/wecom/callback', kind: 'readonly' },
        ].map((r) => ({
          ...r,
          sw: this.sw(r.on === true, accent),
          isSwitch: r.kind === 'switch',
          isInput: r.kind !== 'switch',
          ph: r.ph || '',
          inputStyle: {
            height: '32px', width: r.kind === 'readonly' ? '100%' : '360px', maxWidth: '100%',
            border: '1px solid #DEE0E3', borderRadius: '6px', padding: '0 10px', fontSize: '13px',
            background: r.kind === 'readonly' ? '#F7F8FA' : '#fff',
            color: r.kind === 'readonly' ? '#646A73' : '#1F2329',
            fontFamily: 'ui-monospace,SFMono-Regular,Menlo,monospace',
          },
        })),
      },
    };
  }
}

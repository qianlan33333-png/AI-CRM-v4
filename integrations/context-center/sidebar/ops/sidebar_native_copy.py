"""Native manual-copy primitives extracted from verified daru-ops-console v0.4.4.
No env loader, service startup, CRM database, scheduler or cache is imported.
The owning warehouse supplies build_prompt_runtime_bundle and PRODUCT_FACTS_PATH.
"""

from __future__ import annotations

import json

import re

COPY_SCENE_TEMPLATES = {
  "weekly_review": {
    "label": "周复盘",
    "copy": "{name}，你这周复盘里有个判断，我觉得值得先看。\n\n「{review_value_point}」\n\n这周先做一件事：{review_next_action}。\n\n去小程序看完整复盘，我把为什么先做这一步放里面了。",
    "reason": "有明确复盘判断时，优先提炼关键判断和具体行动；只有信息不足时才退回轻量内容提醒。",
  },
  "content_service": {
    "label": "固定内容服务",
    "copy": "{name}，你之前提到「{topic}」，这次给你推荐《{title}》。\n\n我觉得最值得看的是：{hook}\n\n完整内容和具体判断都在卡片里，分享给你。",
    "reason": "固定内容服务先说清内容理由；没有真实近期线索时不得伪造熟悉感，收尾承接本条具体价值。",
  },
  "activation_recall": {
    "label": "激活与召回",
    "copy": "{name}，最近黄小璨更新了《{title}》，这篇我想推荐给你。\n\n它和你之前关注的「{topic}」有关，最值得看的是：{hook}\n\n点开卡片，可以看完整内容和具体建议。",
    "reason": "激活召回必须先给真实内容价值和具体帮助，再说清点开后能看到什么。",
  },
  "campaign": {
    "label": "阶段 Campaign",
    "copy": "{name}，老黄最近围绕「{campaign}」有个判断我觉得值得单独说一下。\n\n它不是多一个功能或活动，而是能帮你把「{topic}」这件事往前推一步：{hook}。\n\n这周你可以先从 {entry} 试一次，重点看这一点：{next_step}。",
    "reason": "Campaign 要把阶段目标翻译成用户能感知的价值，不写成发布公告。",
  },
  "personal_followup": {
    "label": "个人跟进",
    "copy": "{name}，我看你刚才提到的「{topic}」，这其实已经不是泛泛了解一下了。\n\n我想先确认一句：你现在更想解决的是「{option_a}」，还是「{option_b}」？\n\n这两个方向后面适合的产品和服务不一样。你可以再跟我说说，我按你的情况给你一点更具体的建议。",
    "reason": "个人跟进先确认核心需求和产品兴趣，不直接跳到销售。",
  },
  "sales_assist": {
    "label": "成交辅助",
    "copy": "{name}，你刚才问到「{product_question}」，这个问题我记下了。\n\n按我现在看到的信息，你现在更关键的事，可能还是「{topic}」。所以我不想只跟你聊有没有优惠，先把两件事判断清楚：它到底适不适合你，以及能不能解决你眼下最要紧的问题。\n\n你可以先告诉我：你现在更想确认「{option_a}」，还是「{option_b}」？我按你的情况给你一点更具体的建议。",
    "reason": "成交辅助先看关系、需求明确度和成交阶段；优惠/报名类问题不自动承诺，但要自然回应用户问题并推动下一步判断。",
  },
  "sales_stage_bundle": {
    "label": "成交辅助话术组",
    "copy": "{name}，我想先结合你现在的情况，把最需要确认的一件事聊清楚。\n\n你最近更想解决的是「{topic}」。你可以再跟我说说，眼下最卡的是哪一部分？\n\n我先把这个判断做准，再给你更具体的建议。",
    "reason": "成交辅助话术组先读取完整诊断和近期上下文，再按资料完整度生成诊断确认、产品介绍、成交关单三类参考话术。",
  },
}

SEVERE_COPY_QUALITY_ISSUES = {
  "unresolved_placeholder",
  "legacy_handoff_word",
  "stiff_handoff_expression",
  "internal_ops_terms",
  "unverified_user_content_alignment",
  "ai_report_tone",
  "visible_internal_stage_jargon",
  "non_laohuang_user_visible_phrase",
  "abstract_word_overload",
  "formulaic_not_but_structure",
  "ai_sounding_phrase",
  "performative_memory_claim",
  "stiff_content_service_ending",
  "content_service_unsolicited_profile_question",
  "content_service_weak_generic_ending",
  "content_service_repetitive_card_ending",
  "content_service_cross_content_contamination",
  "service_cross_content_contamination",
  "content_service_wrong_card_label",
  "content_service_multiple_actions",
  "content_service_stiff_self_reference",
  "content_service_unsupported_personal_importance",
  "content_service_unverified_content_detail",
  "content_service_content_fact_blocked_phrase",
  "information_capture_question_count",
  "information_capture_sensitive_field",
  "information_capture_missing_hypothesis_confirmation",
  "information_capture_missing_binary_choice",
  "information_capture_multiple_cta",
  "sales_invented_card_entry",
  "sales_product_override_violation",
  "missing_display_name",
  "garbled_character",
  "suspicious_number_range",
  "nested_title_marks",
  "duplicate_subject",
  "abnormal_punctuation",
  "possibly_truncated_sentence",
  "orphan_connector_opening",
  "strict_avoid_copy_phrase_used",
  "personal_followup_canned_script",
  "personal_followup_ignores_known_need",
  "weekly_behavior_report",
  "weekly_monitoring_tone",
  "weekly_private_opening",
  "weekly_discussion_prompt",
  "weekly_generic_value_placeholder",
  "weekly_canned_low_pressure_ending",
  "weekly_invented_action_without_source",
  "weekly_first_delivery_missing_explanation",
  "weekly_repeat_redundant_explanation",
  "weekly_generic_system_cta",
  "weekly_review_content_semantic_blur",
  "weekly_unframed_internal_summary",
  "weekly_abrupt_instruction",
  "weekly_abstract_value_claim",
  "weekly_missing_card_action",
  "weekly_awkward_content_transition",
  "weekly_repetitive_review_ending",
  "activation_monitoring_tone",
  "activation_generic_ai_question",
  "activation_reply_gate_before_card",
  "activation_fake_personalization",
  "activation_missing_concrete_action",
  "duplicate_salutation",
  "activation_weak_cta",
  "activation_canned_ai_phrase",
  "activation_canned_evaluation",
  "activation_duplicate_action",
  "activation_vague_reassurance",
  "activation_missing_need_anchor",
  "activation_missing_recommendation",
  "activation_formulaic_recommendation",
  "activation_bare_card_delivery",
  "activation_weak_value_reason",
}

def copy_value(data: dict, key: str, fallback: str) -> str:
  value = str(data.get(key) or "").strip()
  return value or fallback

def compact_text_list(value, *, limit: int = 8) -> list[str]:
  if isinstance(value, list):
    items = value
  elif isinstance(value, str):
    items = re.split(r"[；;\n]+", value)
  else:
    items = []
  out = []
  for item in items:
    text = str(item or "").strip()
    if text:
      out.append(text)
    if len(out) >= limit:
      break
  return out

def clean_display_name(value: str) -> str:
  raw = str(value or "").strip()
  if not raw:
    return "宝子"
  if raw in {"客户名", "用户", "问卷提交用户", "未知用户", "待识别用户"}:
    return "宝子"
  if re.search(r"用户[-_a-f0-9]{6,}$", raw, re.I):
    return "宝子"
  if re.fullmatch(r"[0-9a-f-]{8,}", raw, re.I) or re.fullmatch(r"\d{6,}", raw):
    return "宝子"
  if re.fullmatch(r"[\W_]{1,4}", raw):
    return "宝子"
  return raw

def template_strategy_meta(data: dict) -> dict:
  scene = str(data.get("scene") or "content_service")
  topic = copy_value(data, "user_topic", "当前问题")
  product_hint = product_fact_hint(data)
  return {
    "diagnosis_summary": copy_value(
      data,
      "diagnosis_summary",
      f"当前围绕「{topic}」生成参考话术；如果身份、行业、业务形态或需求不清，需要先补诊断。"
    ),
    "conversation_strategy": copy_value(
      data,
      "conversation_strategy",
      "先自然引用用户近期上下文，再用一个具体问题推进到下一步。"
    ),
    "facts_to_confirm_before_send": [
      item for item in [
        scene == "sales_assist" and "价格、优惠、名额、报名路径、个案政策需人工核准",
        product_hint and f"产品事实已装载：{product_hint}",
        "行业和业务形态缺失时，发送前确认是否需要补采",
      ] if item
    ],
    "risk_flags": [],
  }

def copy_render_values(data: dict) -> dict:
  return {
    "name": clean_display_name(copy_value(data, "display_name", "宝子")),
    "title": copy_value(data, "content_title", "这篇内容"),
    "topic": copy_value(data, "user_topic", "你现在关心的问题"),
    "hook": copy_value(data, "value_hook", "这里面有个判断可能能帮你少绕一点路"),
    "entry": copy_value(data, "entry", "黄小璨 AI"),
    "question": copy_value(data, "specific_question", "这跟我现在的情况有什么关系，下一步该先做什么？"),
    "next_step": copy_value(data, "next_step", "先看我标出来的这一段，再把问题放进去问一轮"),
    "campaign": copy_value(data, "campaign_name", "AI + OPC/OPT"),
    "option_a": copy_value(data, "option_a", "方向已经定了、缺具体打法"),
    "option_b": copy_value(data, "option_b", "方向本身还没定"),
    "product_question": copy_value(data, "product_question", "哪种服务更适合我"),
    "review_value_point": copy_value(data, "review_value_point", "卡片里有一条值得你继续看的判断"),
    "review_next_action": copy_value(data, "review_next_action", "先点开卡片看完整建议"),
  }

def product_facts() -> list[dict]:
  try:
    payload = json.loads(PRODUCT_FACTS_PATH.read_text(encoding="utf-8"))
  except Exception:
    return []
  return payload if isinstance(payload, list) else []

def product_fact_for(data: dict) -> dict | None:
  if isinstance(data.get("product_fact"), dict):
    return data["product_fact"]
  key = str(data.get("product_key") or "").strip()
  if not key or key == "none":
    return None
  return next((item for item in product_facts() if item.get("product_key") == key), None)

def compact_product_fact(fact: dict | None) -> dict | None:
  if not fact:
    return None
  qa_limit = 12 if fact.get("product_key") == "soak299" else 4
  return {
    "product_key": fact.get("product_key", ""),
    "product_name": fact.get("product_name", ""),
    "short_name": fact.get("short_name", ""),
    "external_name": fact.get("external_name", ""),
    "internal_name": fact.get("internal_name", ""),
    "status": fact.get("status", ""),
    "price_confirmed": fact.get("price_confirmed", ""),
    "positioning": fact.get("positioning", ""),
    "business_role": fact.get("business_role", ""),
    "philosophy": list(fact.get("philosophy") or [])[:6],
    "core_value": list(fact.get("core_value") or [])[:6],
    "audience": list(fact.get("audience") or [])[:5],
    "rights": list(fact.get("rights") or [])[:8],
    "sales_policy": list(fact.get("sales_policy") or [])[:8],
    "user_scenarios": list(fact.get("user_scenarios") or [])[:6],
    "suitable_for": list(fact.get("suitable_for") or [])[:5],
    "avoid_for": list(fact.get("avoid_for") or [])[:5],
    "fit_decision": fact.get("fit_decision") or {},
    "final_close_sop": fact.get("final_close_sop") or {},
    "common_questions": list(fact.get("common_questions") or [])[:qa_limit],
    "copy_generation_rules": list(fact.get("copy_generation_rules") or []),
    "next_action_policy": fact.get("next_action_policy", ""),
    "redline": list(fact.get("redline") or []),
    "uncertain_items": list(fact.get("uncertain_items") or []),
    "source_paths": list(fact.get("source_paths") or []),
  }

def product_fact_hint(data: dict) -> str:
  fact = compact_product_fact(product_fact_for(data))
  if not fact:
    return ""
  rights = "；".join(list(fact.get("rights") or [])[:3])
  philosophy = "；".join(list(fact.get("philosophy") or [])[:1])
  uncertain = "；".join(fact.get("uncertain_items") or [])
  parts = [
    fact.get("short_name") or fact.get("product_name"),
    fact.get("positioning", ""),
    f"理念：{philosophy}" if philosophy else "",
    fact.get("price_confirmed", ""),
    f"待人工确认：{uncertain}" if uncertain else "",
  ]
  if rights:
    parts.append(f"可讲权益：{rights}")
  return " / ".join([str(item) for item in parts if item])

def fill_copy_placeholders(text: str, data: dict) -> str:
  output = str(text or "")
  values = copy_render_values(data)
  replacements = {
    "{name}": values["name"],
    "{title}": values["title"],
    "{topic}": values["topic"],
    "{hook}": values["hook"],
    "{entry}": values["entry"],
    "{question}": values["question"],
    "{next_step}": values["next_step"],
    "{campaign}": values["campaign"],
    "{option_a}": values["option_a"],
    "{option_b}": values["option_b"],
    "{product_question}": values["product_question"],
    "{review_value_point}": values["review_value_point"],
    "{review_next_action}": values["review_next_action"],
    "{{客户名}}": values["name"],
    "{{内容标题}}": values["title"],
    "{{用户主题}}": values["topic"],
    "{{价值点}}": values["hook"],
    "{{入口}}": values["entry"],
    "{{问题}}": values["question"],
  }
  for token, value in replacements.items():
    output = output.replace(token, value)
  return output

def has_copy_placeholders(text: str) -> bool:
  raw = str(text or "")
  return bool(re.search(
    r"\{[A-Za-z_\u4e00-\u9fff][^{}]{0,24}\}|【[^】]{1,32}(?:标题|链接|日期|时间|地点|姓名|称呼|内容|主题|权益|价格|入口|路径|ID|id)[^】]*】|x月x日|xxxx",
    raw,
    re.I,
  ))

def copy_quality_issues(text: str, scene: str = "", data: dict | None = None) -> list[str]:
  raw = str(text or "")
  context = data or {}
  runtime_bundle = build_prompt_runtime_bundle(context) if context else {}
  relationship_context = runtime_bundle.get("relationship_context") or {}
  information_capture = runtime_bundle.get("information_capture_plan") or {}
  information_capture_enabled = bool(information_capture.get("enabled"))
  personalization_allowed = bool(relationship_context.get("personalization_allowed"))
  trust_state = str(context.get("trust_state") or "")
  semantic_match_status = str(context.get("semantic_match_status") or "unknown")
  issues = []
  if has_copy_placeholders(raw):
    issues.append("unresolved_placeholder")
  if re.search(r"交人", raw):
    issues.append("legacy_handoff_word")
  if re.search(r"不在企微里替你|乱拍|乱答|乱回复|随口|替你下判断|不替你拍|不敢.*定|报错数|过期的信息", raw):
    issues.append("stiff_handoff_expression")
  if re.search(r"潜力用户|意向用户|漏斗|雷达|动作包|归因|监测|legacy_plan|subscription\s+standard", raw, re.I):
    issues.append("internal_ops_terms")
  if (
    scene in {"weekly_review", "content_service", "activation_recall"}
    and semantic_match_status in {"mismatch", "mismatch_suppressed", "unverified"}
    and re.search(r"你之前|你上次|你最近|你正在|你一直|还记得你|上周聊到", raw)
  ):
    issues.append("unverified_user_content_alignment")
  if re.search(r"继续聊聊|有需要可以回复|随时找我", raw) and not re.search(r"先|具体|确认|问", raw):
    issues.append("vague_next_step")
  if re.search(r"系统显示|根据你的画像|以下是|总结如下|作为.*用户|综合来看|基于以上", raw):
    issues.append("ai_report_tone")
  if re.search(r"销售阶段|成交阶段|确认方案这一步|补采|画像|适配度|推进到下一步", raw):
    issues.append("visible_internal_stage_jargon")
  if re.search(r"接住|对个焦|对焦|由系统替你扛|系统替你扛|帮你把下一步对准|帮你把判断做准", raw):
    issues.append("non_laohuang_user_visible_phrase")
  abstract_words = re.findall(r"对焦|适配|路径|抓手|阶段|链路|闭环|沉淀|承接|推进|本质|核心|系统化|转化路径|工作流", raw)
  if len(abstract_words) >= 8:
    issues.append("abstract_word_overload")
  if len(re.findall(r"不是[^。！？\\n]{0,30}而是", raw)) >= 2:
    issues.append("formulaic_not_but_structure")
  if re.search(r"我这边的体感是|想跟你认真对个焦|后面给你的东西才能对得上|这个阶段其实|这件事本质是|(?:挺|很)?(?:对得上|接得上)|挺有意思", raw):
    issues.append("ai_sounding_phrase")
  if bool(context.get("strict_avoid_copy_phrases")) and any(
    str(phrase or "").strip() and str(phrase).strip() in raw
    for phrase in context.get("avoid_copy_phrases") or []
  ):
    issues.append("strict_avoid_copy_phrase_used")
  if (
    scene in {"weekly_review", "content_service", "activation_recall"}
    and re.search(r"我一直记着|这句话我(?:一直)?记着了|我可一直记得", raw)
    and not (personalization_allowed and re.search(r"信任度高|高信任|\bhigh\b", trust_state, re.I))
  ):
    issues.append("performative_memory_claim")
  if re.search(r"对得上你的情况|放回你的|放回自己的|结合(?:你)?自己的情况看看|(?:和|跟)自己(?:当前|现在)?的情况有没有关系|是不是和自己|是否和自己|和自己现在关心|不用把整篇|整篇都看|有具体问题再问我|直接来小程序找我|咱们把.{0,20}放回", raw):
    issues.append("stiff_content_service_ending")
  if scene == "content_service" and not information_capture_enabled and re.search(r"多问一句|有个问题想确认|(?:我推测|我猜想|我猜|我有个猜测)|你目前.{0,18}(?:行业|业务|团队|阶段)|团队大概什么阶段|后续有.{0,12}内容.{0,12}(?:推|发)给你", raw):
    issues.append("content_service_unsolicited_profile_question")
  if information_capture_enabled:
    if len(re.findall(r"[？?]", raw)) != 1:
      issues.append("information_capture_question_count")
    if re.search(r"(?:你|用户).{0,24}(?:收入|营收|家庭|婚姻|孩子|带娃|情绪|疾病|负债|求职|现居|年龄)", raw):
      issues.append("information_capture_sensitive_field")
    capture_mode = str(information_capture.get("mode") or "")
    if capture_mode == "hypothesis_confirm" and not re.search(r"(?:这个猜测|我猜得|我理解得).{0,12}(?:对吗|对不对|准确吗)", raw):
      issues.append("information_capture_missing_hypothesis_confirmation")
    if capture_mode == "binary_choice" and not re.search(r"更关注哪一个|更想先看哪一个|更想先解决哪一个|A.{0,20}B", raw, re.I):
      issues.append("information_capture_missing_binary_choice")
    if re.search(r"点开|打开|点击|进入|卡片|去看|看看|分享给你", raw) and re.search(r"[？?]", raw):
      issues.append("information_capture_multiple_cta")
  if scene in {"weekly_review", "content_service", "activation_recall"}:
    service_content_fact = runtime_bundle.get("content_fact") or {}
    service_conflict_markers = [
      str(item or "").strip()
      for item in service_content_fact.get("conflict_markers") or []
      if str(item or "").strip()
    ]
    if service_conflict_markers and any(marker in raw for marker in service_conflict_markers):
      issues.append("service_cross_content_contamination")
  if scene == "content_service" and re.search(r"卡片(?:我)?(?:放)?在下面|卡片(?:我)?放下面|(?:你)?有空.{0,4}(?:看看|翻翻|看一眼)|我觉得值得看，?先发你了|卡片给你放下面", raw):
    issues.append("content_service_weak_generic_ending")
  if scene == "content_service" and re.search(r"完整(?:拆解|内容|案例)[^。！\n]{0,28}卡片里[^。！\n]{0,24}完整(?:拆解|内容|案例)[^。！\n]{0,28}卡片里", raw):
    issues.append("content_service_repetitive_card_ending")
  if scene == "content_service" and len(re.findall(r"卡片", raw[-120:])) >= 2:
    issues.append("content_service_repetitive_card_ending")
  if scene == "content_service" and re.search(r"点开.{0,30}(?:再点|深入聊聊)|点开卡片.{0,40}(?:继续聊|继续交流)", raw):
    issues.append("content_service_multiple_actions")
  if scene == "content_service" and re.search(r"结合自己的情况看看|结合你自己的情况看看|和自己当下关心的事有关", raw):
    issues.append("content_service_stiff_self_reference")
  if scene == "content_service" and re.search(r"复盘卡片|本周复盘里", raw):
    issues.append("content_service_wrong_card_label")
  if scene == "content_service":
    branch = str((runtime_bundle.get("trace") or {}).get("user_context_branch") or "") if context else ""
    if branch == "low_info" and re.search(r"我觉得.{0,8}对你(?:很|特别)重要|这个研判.{0,12}对你(?:很|特别)重要", raw):
      issues.append("content_service_unsupported_personal_importance")
    content_type = str(context.get("content_type") or "").lower()
    source_content = " ".join(str(context.get(key) or "") for key in [
      "content_title", "value_hook", "content_summary", "content_facts", "local_service_pack_copy_reference", "approved_push_copy",
    ])
    runtime_content_fact = build_prompt_runtime_bundle(context).get("content_fact") or {}
    source_content = " ".join([source_content, json.dumps(runtime_content_fact, ensure_ascii=False)])
    conflict_markers = [str(item or "").strip() for item in runtime_content_fact.get("conflict_markers") or [] if str(item or "").strip()]
    if conflict_markers and any(marker in raw for marker in conflict_markers):
      issues.append("content_service_cross_content_contamination")
    if any(str(phrase or "").strip() and str(phrase).strip() in raw for phrase in runtime_content_fact.get("blocked_phrases") or []):
      issues.append("content_service_content_fact_blocked_phrase")
    if ("observation" in content_type or "observation" in str(context.get("entry") or "").lower()) and (
      re.search(r"(?:它俩|两个平台|两家|不同).{0,20}(?:抢什么|怎么抢|打法|应用场景).{0,8}(?:差异|不同)?|一个在.{0,20}一个在|(?:完整|具体).{0,8}(?:数据|分析|对比).{0,12}(?:放在|卡片|拆好|拆了|拆解)|卡片里.{0,12}(?:对比|差异).{0,8}(?:拆好|拆了|拆解)", raw)
      and not re.search(r"抢什么|怎么抢|打法|应用场景|对比|差异", source_content)
    ):
      issues.append("content_service_unverified_content_detail")
  if scene in {"personal_followup", "sales_assist", "sales_stage_bundle"}:
    if "卡片" in raw and not str(context.get("entry") or context.get("card_path_or_link") or "").strip():
      issues.append("sales_invented_card_entry")
    if str(context.get("_candidate_variant") or "") == "diagnosis_confirm" and len(re.findall(r"[？?]", raw)) > 1:
      issues.append("sales_diagnosis_multiple_questions")
    if (
      str(context.get("product_selection_mode") or "") == "operator_override"
      and str(context.get("product_key") or "") not in {"", "soak299"}
      and re.search(r"(?:先|改|退|回|从).{0,12}299|299.{0,12}(?:更适合|先观察|先订阅)", raw)
    ):
      issues.append("sales_product_override_violation")
  if "�" in raw or re.search(r"装进展\d+天|AI工[，。；\s]", raw):
    issues.append("garbled_character")
  if re.search(r"[一二三四五六七八九十]+六年", raw):
    issues.append("suspicious_number_range")
  if re.search(r"《[^》]*《|》[^《]*》", raw):
    issues.append("nested_title_marks")
  if re.search(r"你这周你|这周你你|我我|用户用户", raw):
    issues.append("duplicate_subject")
  display_name = clean_display_name(context.get("display_name") or "")
  if display_name and re.match(rf"^{re.escape(display_name)}[，,]\s*(?:是把|就是把|而是|所以是)", raw):
    issues.append("orphan_connector_opening")
  if re.search(r"[，。！？；：]{2,}|[,.!?;:]{2,}|[，。！？；：][,.!?;:]", raw):
    issues.append("abnormal_punctuation")
  if re.search(r"(以及|以及其|或者|包括|围绕|关于|工业|合作边|换选|视频号和)[。！？]?\s*(?:\n|$)", raw):
    issues.append("possibly_truncated_sentence")
  if re.match(r"^[^，,。！？\n]{1,24}[，,]\s*(?:而是|但是|所以|不过|然后)", raw):
    issues.append("orphan_connector_opening")
  if re.search(r"(?:^|\n)\s*(?:而是|但是|所以|然后)\s*[^，,。！？\n]", raw):
    issues.append("orphan_connector_opening")
  if scene == "personal_followup":
    if re.search(r"客观[、,，]靠谱[、,，](?:负责任地)?判断|你觉得这样靠谱吗|再跟我说说你的业务和你的顾虑", raw):
      issues.append("personal_followup_canned_script")
    if str(context.get("user_topic") or "").strip() and re.search(r"具体做什么业务|现在卡在哪(?:一步)?还不太清楚", raw):
      issues.append("personal_followup_ignores_known_need")
  if scene == "weekly_review":
    opening = re.split(r"\n\s*\n", raw, maxsplit=1)[0]
    first_delivery = str(context.get("first_delivery") or "unknown")
    safe_first_review_definition = bool(
      first_delivery == "first"
      and re.search(r"第一次|第一份", raw)
      and re.search(r"以后每周|每周都会|每周会", raw)
      and re.search(r"黄小璨\s*AI.{0,30}阅读和交流.{0,30}(?:总结|复盘).{0,20}(?:学习建议|建议)", raw, re.I)
    )
    if first_delivery == "first" and not safe_first_review_definition:
      issues.append("weekly_first_delivery_missing_explanation")
    if first_delivery == "repeat" and re.search(r"第一次给你发|第一份每周复盘|以后每周.{0,12}(?:复盘|总结)", raw):
      issues.append("weekly_repeat_redundant_explanation")
    if re.search(r"读过\s*\d+\s*篇|看过\s*\d+\s*篇|阅读\s*\d+\s*篇|(?:聊了|互动了|提问了)\s*(?:\d+|x)\s*次|活跃度|活跃程度|未活跃", raw, re.I):
      issues.append("weekly_behavior_report")
    if re.search(r"我(?:会|都会|把|看了|看看|根据|结合).{0,24}(?:阅读|交流|读了什么|聊了什么|活跃)|结合你.{0,16}(?:互动|阅读|交流|活跃)情况|系统.{0,12}(?:看到|记录|监测)", raw) and not safe_first_review_definition:
      issues.append("weekly_monitoring_tone")
    if re.search(r"收入|薪资|工资|家庭|婚姻|离婚|孩子|亲子|情绪|焦虑|抑郁|求职|失业|疾病|负债|债务", opening):
      issues.append("weekly_private_opening")
    if re.search(r"复制|直接问|接着聊|继续聊|想聊|我再跟你|我接着|回我|告诉我|(?:请|去|来|记得|需要|要|完成|先)打卡|打个卡|完成讨论", raw):
      issues.append("weekly_discussion_prompt")
    if re.search(r"里面有个判断|它主要在讲|有个判断我觉得值得", raw):
      issues.append("weekly_generic_value_placeholder")
    if re.search(r"完整理由和下一步.{0,10}(?:卡片|里面)|卡片在下面|有空(?:可以)?(?:看看|点开|看一眼)|你方便的时候|你想看的时候|不着急", raw):
      issues.append("weekly_canned_low_pressure_ending")
    if re.search(r"完整复盘在小程序里[，,]?\s*点开卡片看看|完整复盘在(?:下面|卡片里)[^。！\n]{0,24}点开(?:复盘)?卡片|完整复盘和为什么.{0,18}点开卡片", raw):
      issues.append("weekly_generic_system_cta")
    if not re.search(r"点开|点击", raw):
      issues.append("weekly_missing_card_action")
    if re.search(r"复盘里(?:面)?(?:给你留了|有)(?:一篇|一个|一条)", raw):
      issues.append("weekly_review_content_semantic_blur")
    if re.search(r"我印象最深的是这句[：:]\s*(?![「“])", raw):
      issues.append("weekly_unframed_internal_summary")
    if re.search(r"(?:^|\n\s*\n)(?:先不|先别|先不要|这周只|本周只|这周先做|本周先做)", raw) and not re.search(r"我(?:的感觉|觉得|建议|想法).{0,24}(?:先不|先别|先不要|这周只|本周只|这周先做|本周先做)", raw, re.S):
      issues.append("weekly_abrupt_instruction")
    if re.search(r"(?:几个|一些).{0,24}(?:共性)?(?:问题|判断).{0,60}(?:少踩|少走|有些参考|有参考|值得看)", raw):
      issues.append("weekly_abstract_value_claim")
    if re.search(r"它里面|这周先留了?一篇|这周我留了?一篇", raw):
      issues.append("weekly_awkward_content_transition")
    if re.search(r"完整复盘和建议.{0,20}这个判断在复盘", raw):
      issues.append("weekly_repetitive_review_ending")
    if not str(context.get("review_next_action") or "").strip() and re.search(r"本周(?:先)?只做|这周(?:先)?只做|这周先做|先做一件事|先别|先不要|把.{0,24}跑完|不收钱", raw):
      issues.append("weekly_invented_action_without_source")
    display_name = clean_display_name(context.get("display_name") or "")
    if display_name and "@" in display_name:
      tail = raw[len(display_name):].lstrip("，, ") if raw.startswith(display_name) else raw
      if re.match(r"[^，,\n。！？]{1,12}[，,]", tail):
        issues.append("duplicate_salutation")
  if scene == "activation_recall":
    if re.search(r"(?:跟你说个.{0,12})?我最(?:顺手|拿手)的本事|点开(?:后)?先看(?:这|一)条就行|卡片(?:我放|放)?在下面|一句话就行|就行", raw):
      issues.append("activation_canned_ai_phrase")
    if re.search(r"挺(?:有意思|实在)", raw):
      issues.append("activation_canned_evaluation")
    if re.search(r"你哪天|手上正好|拿(?:它|这个|这篇)来试试|说得不完整也没关系|先一起看看|我陪你一起看看", raw):
      issues.append("activation_vague_reassurance")
    if re.search(r"有阵子没看到你|很久没(?:看到你|见你|来用)|最近没怎么用|未活跃|沉默了?\s*\d+\s*天|近\s*999\s*天", raw):
      issues.append("activation_monitoring_tone")
    if re.search(r"这跟我现在的情况有什么关系|这和我现在的情况有什么关系", raw):
      issues.append("activation_generic_ai_question")
    entry = str(context.get("entry") or context.get("card_path_or_link") or "").strip()
    if entry and re.search(r"回我[“\"']?要|你要的话.{0,12}(?:发你|入口)|想看的话回我", raw):
      issues.append("activation_reply_gate_before_card")
    branch = str((runtime_bundle.get("trace") or {}).get("user_context_branch") or "") if context else ""
    if branch == "low_info" and not information_capture_enabled:
      if re.search(r"我猜你(?:现在|最近|正在)|我猜你可能(?:也)?在.{0,12}(?:做|筹备|准备|经营)", raw):
        issues.append("activation_fake_personalization")
    if branch == "evidence_rich" and personalization_allowed and not re.search(r"你之前|你最近|你正在|你刚才|你提到|你现在|你上次|你做了|你一直|你过去|最近在琢磨", raw):
      issues.append("activation_missing_need_anchor")
    if not re.search(r"推荐|建议|值得.{0,4}看|可以看看|我想发你|我把.{0,12}发你|挺适合你|我觉得.{0,24}(?:对你有用|适合你|你会用得上|值得|有启发|有帮助)", raw):
      issues.append("activation_missing_recommendation")
    if re.search(r"所以我建议你(?:先|完整)?(?:看看|看一下|把).{0,8}(?:这期|这一期|这篇)", raw):
      issues.append("activation_formulaic_recommendation")
    if re.search(r"(?:我把|已经把)?卡片(?:一起)?发你了[。！]?$", raw):
      issues.append("activation_bare_card_delivery")
    if not re.search(r"因为|它(?:讲|拆|能|把)|这期.{0,16}(?:讲|拆|能|把)|这个.{0,12}(?:能|会|帮)|背后|趋势|意味着|帮助|帮你|让你|避免|看清|想清楚|讲透|少走|试错|重要|最值得.{0,20}(?:不是|在于)|而是.{0,24}(?:怎么|如何)", raw):
      issues.append("activation_weak_value_reason")
    if branch == "low_info" and re.search(r"你之前提到|我看到这条时就想到你|和你现在的情况.{0,8}(?:接近|有关|对得上)", raw):
      issues.append("activation_fake_personalization")
    display_name = clean_display_name((context or {}).get("display_name") or "")
    second_salutation = None
    if display_name:
      match = re.match(rf"^{re.escape(display_name)}[，,]\s*([^，,\n。！？]{{1,12}})[，,]", raw)
      second_salutation = match.group(1).strip() if match else None
    normal_opening_prefixes = ("这周", "这次", "最近", "今天", "刚刚", "上次", "我", "跟你", "黄小璨", "小程序", "给你", "看到", "想跟你", "有个", "有件", "行业")
    if second_salutation and not second_salutation.startswith(normal_opening_prefixes) and (
      re.fullmatch(r"[A-Za-z\u4e00-\u9fff][A-Za-z0-9_\u4e00-\u9fff-]{1,15}", second_salutation)
    ):
      issues.append("duplicate_salutation")
    if re.search(r"有空(?:可以)?(?:看看|试试|点开|看一眼|翻翻)|感兴趣(?:可以)?看看|有时间看看", raw):
      issues.append("activation_weak_cta")
    has_concrete_action = bool(re.search(
      r"(?:点开|打开|点击|进入).{0,16}(?:卡片|小程序)|"
      r"(?:卡片|这期观察|这篇内容|这个案例|案例卡片).{0,20}(?:点开|打开|点击|进入)|"
      r"(?:点开|打开|进入)后.{0,30}(?:选|挑|看|点)|"
      r"(?:点开|打开|点击|进入).{0,24}(?:完整内容|完整观察|具体判断|具体分析|完整案例|完整复盘)",
      raw,
    ) or re.search(r"卡片里.{0,30}(?:(?:可以|能|值得)(?:看|看到)|值得.{0,6}看)", raw))
    if information_capture_enabled and re.search(r"[？?]", raw):
      has_concrete_action = True
    if not has_concrete_action:
      issues.append("activation_missing_concrete_action")
    if "卡片" in raw and len(re.findall(r"点开|打开|点击|进入", raw)) >= 2:
      issues.append("activation_duplicate_action")
    if re.search(r"点开.{0,30}(?:选|挑).{0,30}再点|点开.{0,50}我想聊聊这一条", raw):
      issues.append("activation_duplicate_action")
  return issues

def service_safe_topic(value) -> str:
  """Keep a service-relevant topic without exposing a whole profile or sensitive facts."""
  raw = " ".join(str(value or "").replace("用户最近主动表达：", "").split())
  if not raw:
    return ""
  patterns = [
    r"核心诉求(?:是|为)\s*([^；。]{3,38})",
    r"主要(?:诉求|关注|落点)(?:是|为|还是|在于)?\s*([^；。]{3,38})",
    r"正在纠结\s*([^；。]{3,36})",
    r"存在\s*([^；。]{3,30})短板",
    r"我想做\s*([^；。？]{3,28})",
  ]
  sensitive = r"收入|营收|家庭|婚姻|孩子|带娃|情绪|疾病|负债|求职|现居|年龄|\d+\s*岁"
  for pattern in patterns:
    match = re.search(pattern, raw)
    if not match:
      continue
    topic = match.group(1).strip(" ，。；：‘’“”\"'")
    if topic and not re.search(sensitive, topic):
      return topic[:48].rstrip("，。；：")
  profile_markers = ["是资深", "是成熟", "是处于", "年收入", "现居", "拥有", "核心诉求", "短板"]
  if len(raw) <= 48 and not re.search(sensitive, raw) and not any(marker in raw for marker in profile_markers):
    return raw
  return ""

def compact_hxc_service_context(value) -> dict:
  """Keep only confirmed, non-private HXC service continuity fields for copy generation."""
  source = value if isinstance(value, dict) else {}
  focuses = []
  for item in source.get("focuses") or []:
    if not isinstance(item, dict) or str(item.get("priority") or "") not in {"primary", "secondary"}:
      continue
    summary = str(item.get("safe_summary") or "").strip()
    if not summary:
      continue
    focuses.append({
      "priority": str(item.get("priority") or ""),
      "safe_summary": summary[:180],
      "current_conclusion_safe": str(item.get("current_conclusion_safe") or "").strip()[:240],
      "progress_state": str(item.get("progress_state") or "")[:40],
    })
  advice = []
  allowed_advice_states = {"proposed", "delivered", "accepted", "adopted", "effective", "ineffective"}
  for item in source.get("advice") or []:
    if not isinstance(item, dict) or str(item.get("advice_status") or "") not in allowed_advice_states:
      continue
    advice.append({
      "support_type": str(item.get("support_type") or "")[:60],
      "advice_status": str(item.get("advice_status") or ""),
      "occurred_at": str(item.get("occurred_at") or "")[:40],
    })
  commitments = []
  for item in source.get("commitments") or []:
    if not isinstance(item, dict):
      continue
    summary = str(item.get("safe_summary") or "").strip()
    if not summary:
      continue
    commitments.append({
      "safe_summary": summary[:180],
      "status": str(item.get("status") or "")[:40],
      "due_at": str(item.get("due_at") or "")[:40],
    })
  quality = {}
  for dimension, item in (source.get("service_quality") or {}).items():
    if dimension not in {"accuracy", "value", "continuity", "boundary", "closure"} or not isinstance(item, dict):
      continue
    level = str(item.get("signal_level") or "")
    if level not in {"green", "yellow", "red"}:
      continue
    quality[dimension] = {
      "signal_level": level,
      "reason_code": str(item.get("reason_code") or "")[:80],
    }
  return {
    "confirmed_focuses": focuses[:3],
    "advice_outcomes": advice[:5],
    "ai_commitments": commitments[:5],
    "service_quality": quality,
    "usage_boundary": [
      "只用于保持服务连续、避免重复无效建议和调整语气，不自动改变产品推荐。",
      "不得把 proposed/delivered 写成用户已认可；只有 accepted/adopted/effective 可视为正向结果。",
      "accuracy=red 时不得引用旧判断装熟；closure=red 时优先把已承诺事项说明或补齐。",
      "不得向用户透露内部质量标签、建议状态或承诺状态字段名。",
    ],
  }

def copy_generation_prompt(data: dict) -> str:
  scene = str(data.get("scene") or "content_service")
  template = COPY_SCENE_TEMPLATES.get(scene, COPY_SCENE_TEMPLATES["content_service"])
  product_fact = compact_product_fact(product_fact_for(data))
  if scene == "activation_recall" and str(data.get("activation_mode") or "service_reactivation") == "service_reactivation":
    product_fact = None
  data_gaps = compact_text_list(data.get("data_gaps") or data.get("unknowns") or data.get("required_info_to_collect"))
  runtime_bundle = build_prompt_runtime_bundle(data)
  relationship_context = runtime_bundle.get("relationship_context") or {}
  known_facts = compact_text_list(data.get("known_facts") or data.get("user_known_facts") or data.get("evidence"))
  service_scenes = {"weekly_review", "content_service", "activation_recall"}
  if scene in service_scenes:
    known_facts = [item for item in known_facts if not re.search(r"收入|营收|家庭|婚姻|孩子|带娃|情绪|疾病|负债|求职|现居|年龄", item)]
  if scene == "content_service":
    known_facts = known_facts[:1] if relationship_context.get("personalization_allowed") else []
  elif scene == "weekly_review":
    known_facts = known_facts[:2] if relationship_context.get("personalization_allowed") else []
  else:
    known_facts = known_facts[:3]
  runtime_examples = list(runtime_bundle.get("selected_examples") or [])
  hxc_service_context = compact_hxc_service_context(data.get("hxc_service_context"))
  supplied_voice_samples = compact_text_list(data.get("voice_reference_samples") or data.get("style_reference_samples"), limit=5)
  routed_voice_samples = [str(item.get("text") or "").strip() for item in runtime_examples if str(item.get("text") or "").strip()]
  payload = {
    "prompt_runtime": runtime_bundle.get("trace") or {},
    "runtime_instructions": runtime_bundle.get("instructions") or [],
    "runtime_reference_examples": runtime_examples,
    "hxc_service_context": hxc_service_context,
    "card_capability": runtime_bundle.get("card_capability") or {},
    "content_fact": runtime_bundle.get("content_fact") or {},
    "angle_plan": runtime_bundle.get("angle_plan") or {},
    "relationship_context_policy": relationship_context,
    "information_capture_plan": runtime_bundle.get("information_capture_plan") or {},
    "service_match_card": runtime_bundle.get("service_match_card") or {},
    "scene": scene,
    "scene_label": template["label"],
    "scene_specific_rules": [
      "核心口径：不说系统看到了什么，只说这周给用户留了什么。",
      "review_scene=review_action：这是最高优先级。review_value_point 和 review_next_action 来自黄小璨真实复盘，必须保留其具体判断与行动，不得退化为推荐一篇内容，也不得改写成泛泛的学习建议。",
      "review_action 的结构：自然说明复盘整理好了 -> 单独点出一个关键判断 -> 明确说这周只做哪一件事 -> 邀请点开卡片看完整复盘。语言要像熟人私聊，短句、直接、有判断。",
      "参考真实结构：‘你这周复盘里这句是关键：{具体判断}。先不换方向。本周只做一件事：{具体动作}。去小程序看复盘，我把为什么是这个方向放里面了。’只学结构和语感，不照抄内容。",
      "禁止汇报读过/看过多少篇内容；禁止评价活跃程度；禁止说我看了你的阅读/交流/活跃情况。",
      "不得使用收入、家庭、婚姻、亲子、情绪、求职、疾病、负债等私密信息作为主动触达开场。",
      "review_scene=explicit_business_topic：只轻量引用用户主动表达过的非敏感业务/学习主题，再说内容为什么值得看。",
      "first_delivery=first：必须先解释这是第一次给用户发每周复盘；以后每周，黄小璨 AI 都会结合用户在小程序里的阅读和交流，整理一份总结和学习建议。这里只说明服务机制，不得汇报次数、篇数或评价活跃程度。",
      "first_delivery=repeat：不要重复解释周复盘机制。first_delivery=unknown：不得自称第一次，只能中性说明本周复盘已经生成。",
      "review_scene=first_or_low_info：这是复盘深度/信息量分支，不等于首次。不要硬做个性化；先让用户知道收到的是复盘，再说复盘中推荐了什么、具体能帮他判断什么。",
      "信息少时也不能只写‘里面有个判断’或‘它主要在讲’；必须写出真实标题和一个具体判断或适用场景。",
      "review_scene=ongoing_business_topic：只有当用户近期多次主动表达同一非敏感课题时才可使用；必须指出内容里的具体业务判断，再邀请查看完整复盘。",
      "每条只保留一个低压力动作：点开卡片查看完整建议。不要求回复、打卡或讨论，不提供复制式讨论提示词。",
      "不要直接贴内部行为摘要。写‘我印象最深的是这句’时，后面必须是自然引语；不要写成‘这句：你主动要求聊……’。",
      "给行动建议前要有真人判断桥梁，如‘我觉得’‘我的感觉是’‘我的建议是’，不能从摘要突然跳到‘先别/只做一件事’。",
      "禁止‘复盘里给你留了一篇’和‘完整复盘在小程序里，点开卡片看看’。周复盘是总结和学习建议，内容推荐只是复盘的一部分。",
      "收尾必须只有一个自然动作。可以说‘完整内容和这周的学习建议，都在下面的复盘卡片里了’，或‘我把这个判断和后面的建议都放在复盘里了，点击下面的卡片去看看吧’；不要同时重复‘完整复盘、下面、卡片、点开、完整分析’。",
      "必须使用 local_service_pack_copy_reference 里真实存在的文章或专题及其建议；文章标题只保留一层书名号，专题使用专题「名称」，不编造内容观点。",
      "生成后检查标题嵌套、句子截断、重复主语、标点异常、监控感、私密信息开场和复制式讨论提示词；命中任一项就设置 blocked=true。",
      "所有结果只是待审候选，未经人工审核不得发送。",
    ] if scene == "weekly_review" else ([
      "这是固定内容服务，不是诊断或成交推进。默认只说清为什么把内容发给用户；只有 information_capture_plan.enabled=true 时，才允许在创造价值后自然确认一项关键信息。",
      "push_copy 第一行必须以 display_name 开头，不能省略称呼。",
      "只有 recent_context 或 signal_snapshot 里存在用户近期主动表达过的同一非敏感课题时，才可自然提到该课题；否则直接讲内容价值，不得写“你之前提到/我想到你”。",
      "information_capture_plan.enabled=false 时只保留一个卡片动作，不追加资料问题。enabled=true 时只使用计划内的猜测确认或二选一，确认问题是本条唯一 CTA；不再同时催点击、打卡或继续追问。",
      "hypothesis_confirm 优先自然表达为‘我推测/我猜想，你可能正在关注……，这个猜测对吗？’；也可以确认一个已有画像候选。binary_choice 优先表达为‘有个问题想确认下：A 和 B，这两个话题你当前更关注哪一个？’。不得逐字批量套模板。",
      "service_only 模式的结尾要承接本条具体判断，可以说完整内容或具体分析在卡片里。补信息模式不得再提点开/打开/卡片，内容随消息附上即可，最后只保留确认问题。禁止批量套用‘完整理由和下一步都在卡片里’‘卡片在下面，有空看看’‘不着急’。",
      "信息少时也不能只写‘里面有个判断’或‘它主要在讲’；必须写出真实标题和一个具体判断或适用场景。",
      "推荐理由必须来自 content_title、value_hook 或真实内容事实；不编造文章观点，不使用隐私信息开场。",
    ] if scene == "content_service" else ([
      "这是激活/召回服务，不是辅助谈单。先用一条真实内容创造价值，让用户自然恢复一次使用；不得推产品、制造失联焦虑或汇报沉默天数。",
      "information_capture_plan.enabled=false 时，必须只保留一个卡片查看动作，不追加提问。",
      "information_capture_plan.enabled=true 时，先讲清本次内容的具体价值，再使用计划内的猜测确认或二选一；确认问题是唯一 CTA，正文不得再出现点开、打开、点击、进入、卡片或要求填写问卷。",
      "hypothesis_confirm 只能使用 candidate_hypothesis；binary_choice 只能使用 choices 中的两个方向，不得从用户身份、收入、地区、家庭或其他私密信息临时编造猜测。",
      "补采不是固定尾巴；本次内容和缺口无关、近期已问过同一字段、或内容价值说不清时，应保持 service_only。",
      "无论是否补采，都必须写出一个真实内容判断及其帮助，不能只说值得看、有启发或可能有用。",
    ] if scene == "activation_recall" else ([
      "这是个人跟进，不是群发内容，也不是成交三件套。只输出一条自然、可供人工审核的下一步沟通话术。",
      "先读取已确认的 current_need、business_shape、identity_stage 和近期证据；只自然带入最相关的 1-2 个事实，不得逐项复述画像。",
      "如果 current_need 已明确，就围绕该需求给一个具体判断，再确认一个真正影响下一步的变量；不得退回泛问‘你做什么业务、有什么顾虑’。",
      "如果资料仍少，只问一个具体问题。优先问现在更想先解决哪件事、已经尝试过什么、或当前最卡在哪个具体动作，不要让用户重新介绍全部背景。",
      "当两个已确认字段指向不同课题时，不要擅自判断哪个更重要；用一个自然的优先级问题确认用户当前先处理哪一个。",
      "禁止套用‘我可以帮你客观、靠谱、负责任地判断’‘你觉得这样靠谱吗’‘再跟我说说你的业务和你的顾虑’。这些原话只能作为历史风格参考，不能成为固定结尾。",
      "问题前必须先创造一点价值：可以指出一个值得关注的矛盾、风险或选择，但不能把模型推测写成确定诊断。",
      "整条只保留一个问句；不用产品卡片，不推品，不承诺结果。",
    ] if scene == "personal_followup" else ([
      "这是辅助谈单话术组，不是普通推送文案。先读取诊断、关系、近期聊天、行业、收入/营收阶段、业务形态、需求和产品事实。",
      "data_readiness=insufficient 时，只输出 1 个 variant=diagnosis_confirm 的候选，用于补充信息或建立信任；禁止输出产品介绍和成交关单话术。",
      "data_readiness=partial 或 clear 时，输出且只输出 3 个候选，variant 必须依次为 diagnosis_confirm、product_intro、closing。",
      "sales_stage_selection_mode=operator_override 表示大如本次主动改选沟通阶段：必须按 operator_selected_sales_stage 生成，同时在诊断中保留 detected_sales_stage 作对照，不得擅自切回系统阶段。",
      "operator_supplemental_context 是大如本次临时补充的信息：可用于生成，但要标记待核对；如果可沉淀为画像，必须写入 profile_update_candidates 由大如确认。",
      "diagnosis_confirm：自然复述已知事实和判断，只问一个最值得确认的问题；用于信息采集、建立信任或确认需求。",
      "product_intro：结合用户 2-3 个具体事实说明为什么推荐当前产品、能解决什么、可能不适合什么，并只留一个明确动作。",
      "closing：仅在当前阶段允许推进时生成；清楚说明已确认价格/公开权益和下一步。若尚不适合关单，要明确写成“暂不建议发送”，不要伪造催单话术。",
      "三条内容必须角度不同，不能只是替换开头；每条都要在 operator_note 写明适用条件和发送前要核准的事实。",
      "diagnosis_summary 要说明资料是否足够、当前沟通阶段和产品方向；conversation_strategy 要告诉大如本次先发哪一条、为什么。",
    ] if scene == "sales_stage_bundle" else [])))),
    "goal": data.get("goal") or "",
    "display_name": clean_display_name(data.get("display_name") or ""),
    "user_topic": service_safe_topic(data.get("user_topic")) if scene in service_scenes else (data.get("user_topic") or ""),
    "content_title": data.get("content_title") or "",
    "value_hook": data.get("value_hook") or "",
    "entry": data.get("entry") or "",
    "specific_question": data.get("specific_question") or "",
    "recent_context": relationship_context.get("recent_context") or "",
    "long_term_hxc_summary": relationship_context.get("memory_excerpt") or "",
    "identity_stage": data.get("identity_stage") or "",
    "income_stage": data.get("income_stage") or "",
    "industry": data.get("industry") or "",
    "business_shape": data.get("business_shape") or "",
    "relationship_state": data.get("relationship_state") or "",
    "trust_state": data.get("trust_state") or "",
    "intimacy_state": data.get("intimacy_state") or "",
    "operating_segment": data.get("operating_segment") or "",
    "operating_segment_basis": data.get("operating_segment_basis") or "",
    "relationship_evidence": relationship_context.get("selected_evidence") or [],
    "service_feedback_history": data.get("service_feedback_history") or [],
    "interaction_highlights": compact_text_list(data.get("interaction_highlights"), limit=6),
    "recent_touch_history": compact_text_list(data.get("recent_touch_history"), limit=8),
    "contact_preference": data.get("contact_preference") or "",
    "content_type": data.get("content_type") or "",
    "recommended_content_id": data.get("recommended_content_id") or "",
    "recommended_content_title": data.get("recommended_content_title") or "",
    "activation_mode": data.get("activation_mode") or "",
    "batch_index": data.get("batch_index"),
    "batch_size": data.get("batch_size"),
    "diversity_slot": data.get("diversity_slot"),
    "diversity_angle": data.get("diversity_angle") or "",
    "product_key": data.get("product_key") or "",
    "product_selection_mode": data.get("product_selection_mode") or "system_recommendation",
    "product_fact": product_fact,
    "product_question_bank": product_fact.get("common_questions") if product_fact else [],
    "copy_angle": data.get("copy_angle") or "",
    "copy_angle_reason": data.get("copy_angle_reason") or "",
    "signal_snapshot": data.get("signal_snapshot") or "",
    "signal_collection_plan": data.get("signal_collection_plan") or "",
    "sales_stage": data.get("sales_stage") or "",
    "sales_stage_selection_mode": data.get("sales_stage_selection_mode") or "system_diagnosis",
    "detected_sales_stage": data.get("detected_sales_stage") or "",
    "operator_selected_sales_stage": data.get("operator_selected_sales_stage") or "",
    "operator_supplemental_context": data.get("operator_supplemental_context") or "",
    "user_current_question": data.get("user_current_question") or data.get("product_question") or "",
    "known_facts": known_facts,
    "data_readiness": data.get("data_readiness") or ("clear" if not data_gaps else "partial"),
    "data_gaps": data_gaps,
    "required_info_to_collect": compact_text_list(data.get("required_info_to_collect") or data_gaps),
    "effective_period": data.get("effective_period") or "",
    "suggested_send_at": data.get("suggested_send_at") or "",
    "local_service_pack_copy_reference": "" if scene == "activation_recall" else (data.get("approved_push_copy") or ""),
    "review_scene": data.get("review_scene") or "",
    "review_depth": data.get("review_depth") or "",
    "review_value_point": data.get("review_value_point") or "",
    "review_next_action": data.get("review_next_action") or "",
    "first_delivery": data.get("first_delivery") or "unknown",
    "voice_reference_samples": (supplied_voice_samples + routed_voice_samples)[:5],
    "business_boundary": [
      "不自动发送，只生成候选话术。",
      "周复盘、固定内容服务、激活/召回和 Campaign 必须先通过 service_match_card：用户背景、需求或可信假设、推荐理由、具体帮助和内容亮点缺一不可；这些是内部证据，不得照着字段写成四段报告。",
      "成稿只自然串起最相关的一个背景事实、一个需求、一个推荐判断和一个具体帮助。若 service_match_card.status=low_info_ready，要诚实降低个性化；若为 hypothesis_ready，必须把需求当猜测而不是事实。",
      "黄小璨记忆原则：宁可少认，不可装懂；只能引用输入里已装载的 user_background、long_term_hxc_summary、recent_context、product_fact 和历史样本。",
      "如果缺行业、业务形态、业务阶段、当前需求或长周期记忆，不要假装熟悉用户；把缺口写进 facts_to_confirm_before_send。周复盘不补采；固定内容服务和激活只有 information_capture_plan.enabled=true 时，才可在先创造价值后确认一个非敏感字段。",
      "黄小璨长周期记忆的用途：识别用户是谁、卡在哪、什么话对他管用、此前是否认可过建议；不能拿来编不存在的私密细节。",
      "可以回应价格、报名、优惠问题，但不能自动承诺具体优惠、名额、结果或非公开政策。",
      "问价、报名、优惠、退款、FDE、联盟、复杂权益必须进入人工审核；话术要服务人工判断，而不是机械说“转人工”。",
      "涉及价格/优惠时，回复要清晰、明确、专业：已确认价格和公开权益可以直接讲清楚；动态优惠、名额、报名入口要写成“人工核准后给准确信息”，不能遮遮掩掩。",
      "禁止写“我不在企微里替你乱拍/乱答/乱回复/随口说/随口给/替你下判断/不替你拍/不敢给你定/别给你报错数/别让你拿到过期信息”这类生硬自我辩解。",
      "成交敏感问题的正确处理是：先回顾用户关系和已知需求 -> 判断需求是否明确 -> 判断当前沟通阶段 -> 给一个自然的下一步确认问题。",
      "下一步必须具体，不能只写继续聊聊。",
      "开头必须自然带入 1-3 个已知事实，例如身份/阶段、业务形态、最近交流、使用记录、需求线索或关系状态；不要写字段清单，不要用“根据你的画像/系统显示/数据显示”这种报告腔。",
      "如果没有足够事实，开头要诚实低信息量，不能硬编熟悉感；可以说“我这边先按你目前留下的信息给你对一下”。",
      "结尾如果推荐课程、活动或产品，必须基于用户 2-3 个具体点说明为什么推荐；固定内容服务只需说清一个具体内容价值，并保留查看卡片这一个动作。不能只写“想了解找我/有需要回复”。",
      "信息清晰的人：可以生成可审核后直接发送的话术，并在 generation_reason 说明使用了哪些事实。",
      "信息不清晰的人：也可以生成一条补采型参考话术，但必须把缺什么写进 facts_to_confirm_before_send 和 candidate.required_info_to_collect，并在 candidate.operator_note 告诉大如先补哪些字段。",
      "如果聊天过程中采集到新信息，不能直接覆盖用户画像；输出 profile_update_candidates，标明字段、候选值、来源、置信度和是否需要人工确认。",
      "profile_update_candidates.field 只能使用 identity_stage、income_stage、industry、business_shape、business_stage、current_need、product_interest、decision_timing、decision_role、relationship_note。",
      "product_selection_mode=operator_override 表示大如本次主动改选产品：必须装载并围绕该 product_fact 判断和生成，不得擅自切回 299；如果不适合，要明确告诉大如为什么不适合，而不是硬写推品话术。",
      "sales_stage_selection_mode=operator_override 表示大如本次主动改选沟通阶段：必须围绕 operator_selected_sales_stage 和 sales_stage 生成，不得因 detected_sales_stage 较早就自动退回；但如果事实不足以支持该阶段，必须在 operator_note 和 facts_to_confirm_before_send 中明确告警。",
      "operator_supplemental_context 只是本次人工补充，不能当成已沉淀的长期画像；可沉淀的新信息必须输出为 profile_update_candidates，等人工确认。",
      "生成候选前必须先做原话风格对照：从 voice_reference_samples、historical_success_patterns、sales_progression_framework 中选择 1-3 个表达锚点，只学习语感、结构和判断方式，不照抄旧价格或旧链接。",
      "生成后必须做一次强去 AI 味重写：先在心里写初稿，但 final push_copy 只能输出重写后的版本。删掉报告腔、客服腔、解释腔、过度完整的总分总、内部阶段术语和抽象名词堆叠；保留具体事实、判断、一个问题或一个动作。",
      "老黄/黄小璨用户可见话术绝对不要写这些词：接住、对个焦、对焦、由系统替你扛、系统替你扛。替换方式：用“我还记得”“我看到了”“这个问题我记下了”“我觉得这里挺好”“你可以再跟我说说”这类更自然的说法。",
      "学习用户给出的改写风格：多用“我觉得/你要知道/有时候/要不然/你怎么想/你觉得这样靠谱吗”；少用模型式总结；允许留白，允许一句话单独成段。",
      "表达判断时不要摆成报告：先自然说用户正在做什么，再给一个判断，再留一个真实问题；不要把用户逼进二选一。",
      "用户可见话术禁用内部阶段词：销售阶段、成交阶段、确认方案这一步、补采、画像、漏斗、适配度、推进到下一步。可以在策略字段里写，不许写进 push_copy。",
      "少用抽象词。对焦、适配、路径、抓手、阶段、链路、闭环、沉淀、承接、推进、本质、核心、系统化、转化路径、工作流等词，一条用户可见消息最多保留 3 个；能换成具体动作就换成具体动作。",
      "避免 AI 常见句式：不是 A 而是 B、这件事本质是、这个阶段其实、我这边的体感是、想跟你认真对个焦、后面给你的东西才能对得上你的事、我先把这个问题接住、我按这个帮你把下一步对准。",
      "内容服务不要为了热闹刻意制造互动。禁用“对得上你的情况/放回你的情况或事里/和自己的情况有没有关系/不用把整篇都看完/有具体问题再问我/直接来小程序找我/咱们把它放回……”等句式。只有信息确认路由明确开启时才问一个具体问题；否则推荐理由说清后自然递出内容。",
      "强去 AI 味后的句子要更像微信：短句、能断行、不面面俱到、允许留白；不要每段都解释为什么。",
      "candidate.voice_reference_used 要写明对照了哪类原话锚点；candidate.voice_alignment_note 要说明哪些地方像、哪些地方需要人工改。",
      "最终 push_copy 严禁保留 {name}、{title}、{topic}、{{客户名}} 等任何占位符，必须替换为输入里的真实内容。",
      "不要暴露标签、漏斗、动作包、归因、监测、潜力用户、意向用户等内部词。",
      "参考模板只能学习结构，不能照抄句式；不要连续使用“这周看到/这个点/挺接得上/不用全看完/直接问”这一套固定骨架。",
      "有抓手的人必须先引用用户自己的事实；无抓手的人才允许通用价值开场，不能硬凑个人痛点。",
      "昵称必须是清洗后的自然称呼；如果是用户 ID、纯数字、手机号或乱码，称呼用“宝子”。",
      "如果用户信息不足，要写成低信息量但诚实的服务提醒，不能假装和用户很熟。",
      "同一批用户不能只换昵称和标题，必须按 copy_angle 分散切入角度。",
      "每条话术必须说明生成前读取的 signal_snapshot，并给出发送后 signal_collection_plan。",
      "如果 local_service_pack_copy_reference 存在，只能把它当作本地服务包参考文案；必须重新生成，不能原样返回。",
      "如果 product_fact 存在，所有产品介绍、权益、价格、退款和适合人群只能来自 product_fact；product_fact.uncertain_items 里的事项必须转人工。",
      "如果 product_fact.fit_decision 存在，必须先按“适合/不适合/先养着”判断，再决定是推品、继续诊断、还是只做关系维护。",
      "如果 product_fact.final_close_sop 存在，最终成交类话术必须按 SOP 生成：确认意向、核准入口/价格/优惠、付款后承接、记录回填。",
      "299 可以明确说已确认价格和权益；但不能承诺效果，不能把 149 升级当作标配，不能把高客单意向用户拉回 299。",
    ],
    "prompt_routing_dimensions": {
      "by_operating_goal": [
        "召回：先给一个低压力重新接上的理由，优先用更新内容、相似案例或用户过去关心的问题，不直接推高客单。",
        "种草：围绕一个明确产品价值或习惯建立共识，重点是让用户理解为什么这件事和自己有关。",
        "成交：先做关系、需求、产品适配和当前阶段判断，再给推进策略和参考回复。",
        "稳定活跃：让用户持续使用黄小璨 AI、每周复盘、内容服务和学习计划，增强信任度和亲密度。",
      ],
      "by_scene": [
        "服务：先提供确定价值，再轻问反馈或下一步。",
        "咨询：先回应问题，再确认具体背景和决策约束。",
        "主动唤醒：先说明为什么这次想到他，不要像群发。",
        "成交辅助：不只出单条话术，必须先输出综合诊断、沟通策略、参考回复、发送前核准项和风险提示。",
      ],
      "by_product": [
        "所有产品信息统一来自 product_fact 和 product_question_bank；不要让模型编产品差异。",
        "299 是日常服务、信任和用户分流层；陌生/未付费/过期用户可种草或续费承接。",
        "4999 工作坊是个人 AI 化和超级 OPC/OPT 工作方式体验层；适合非技术极客、老板/高管、成熟 OPC、有业务但 AI 放大效率不足的人。",
        "FDE 是业务 AI 化改造层；适合想做企业落地、业务 AI 化或商业能力成熟的 OPC。",
        "联盟是长期同行、生态共创和项目孵化层；可以从工作坊/FDE 高适配用户中向上筛选。",
      ],
      "by_recent_context": [
        "必须看近期沟通是否顺畅、是否聊得开心、是否认可过建议、是否咨询过其他产品、这周是否已触达过。",
        "同一批用户不同阶段不能重复同一套骨架；要自然衔接最近几次触达，避免话题跳跃或互相冲突。",
        "如果缺少行业或业务形态，下一步优先补采：他做什么行业、什么业务形态、当前业务在哪个阶段。",
        "黄小璨小程序长周期对话摘要优先作为每周固定更新信号，用于修正需求、信任度、亲密度和产品方向。",
      ],
    },
    "target_user_and_signal_definition": [
      "已确认目标用户：身份阶段和收入/营收阶段都命中目标画像。",
      "疑似目标用户：身份阶段或收入/营收阶段只命中一个，不丢弃，进入补采。",
      "潜力用户：首先是已确认或疑似目标用户，其次需求明确，并且匹配产品方向。",
      "意向用户：画像精准、需求明确，并且有主动咨询、问价、报名、优惠、时间、适配或产品兴趣等信号。",
      "产品方向待定的用户，不进入成交推进；继续诊断，重点补行业、业务形态、业务阶段和具体需求。",
    ],
    "sales_progression_framework": [
      "基础信息收集与建立信任：先提供一个有价值判断或内容，不急着问预算和推产品。",
      "确认需求：用 1 个问题或 2-3 个选项确认真实需求、卡点、时间点和决策角色。",
      "给出解决方案：把用户需求翻译成对应产品/服务的具体价值和使用场景。",
      "确认方案：说明匹配点、仍需确认点和不适合时的退路，降低用户防御。",
      "直接推品：只有关系/信任较好、需求明确、产品适配清楚时，才给明确推荐。",
      "消除异议：回应优惠、价格、时间、是否适合等疑问，先回到“这件事是否真的适合你”，再进入人工确认。",
      "最终成交：报名、优惠、名额、付款等由人工确认；AI 可以生成参考回复草稿，但必须标注为人工审核后发送。",
    ],
    "staged_sales_generation": {
      "stage_1_info_trust": "信息收集和建立信任：适用于信息不足、关系弱、刚进线；只补画像和给价值，不推品。",
      "stage_2_need_confirm": "确认需求：适用于用户有痛点但需求域不清；只问一个推进问题或给 2-3 个选项。",
      "stage_3_solution": "给出解决方案：适用于需求基本明确；先讲解决路径，再让产品作为路径的一部分出现。",
      "stage_4_plan_confirm": "确认方案：适用于方向感兴趣但适配未定；输出匹配点、仍需确认点和不适合退路。",
      "stage_5_product_recommend": "直接推品：适用于关系较好、需求明确、产品适配清楚；坚定推荐但不虚假稀缺。",
      "stage_6_objection": "消除异议：适用于问价格、优惠、时间、是否适合；先判断异议背后的适配/确定性/决策风险，再给清楚回复。",
      "stage_7_close": "最终成交：适用于已决定报名/购买/续费；输出成交承接策略、核准清单和付款后服务承接。",
      "rule": "如果 sales_stage 为空，必须先在 conversation_strategy 里判断当前更像哪一阶段；只生成当前阶段往下一步推进的话术，不一次跨多级。",
    },
    "relationship_and_need_check": [
      "先判断关系：是否付费过、当前是否在服务中、历史建议是否被认可、是否聊过较私密/具体问题。",
      "再判断需求：用户是否说清楚具体业务、目标、卡点、时间窗口、预算/资源约束和决策权。",
      "如果关系弱或需求不清，话术目标是补信息和建立信任；不要直接推高客单。",
      "如果关系强且需求清楚，话术可以更确定，但仍要给用户一个具体判断依据，不要靠情绪催单。",
    ],
    "opening_and_closing_rules": {
      "natural_opening": [
        "第一句要像真实企微对话，自然串起用户事实，不要模板化复述。",
        "优先引用：最近一次聊天/打开/复盘事实、行业或业务形态、收入/业务阶段、已知关注方向、此前关系或付费状态。",
        "同批用户不能只换称呼；每个人的第一句至少要有一个不同的事实抓手。",
      ],
      "explicit_recommendation_ending": [
        "如果要推内容/课/活动/产品，结尾必须说清：基于 A、B、C，所以建议你先关注 X。",
        "推荐理由要落到用户当前阶段的实际问题，不能只讲产品好。",
        "只保留一个动作入口：回复、打开卡片、读某一段、问某个具体问题或让大如发报名/权益信息。",
      ],
      "data_clarity_branch": [
        "data_readiness=clear：输出可审核后直接发送的话术。",
        "data_readiness=partial：输出话术 + operator_note，说明发送前/发送后优先确认的 1-3 个字段。",
        "data_readiness=insufficient：不要推产品；只做轻服务或补采，重点让用户补行业、业务形态、业务阶段、当前问题。",
      ],
      "profile_update_loop": [
        "当用户回复里出现行业、业务形态、业务阶段、收入/营收、产品兴趣、预算、时间点、决策角色、信任/认可证据时，生成 profile_update_candidates。",
        "问卷、订单、黄小璨结构化字段可高置信更新；聊天抽取默认为待确认，需大如审核或二次证据后进入正式画像。",
        "下一次生成话术必须读取更新后的用户状态卡和触达历史，避免前后矛盾。",
      ],
    },
    "historical_success_patterns": ([
      "本轮激活采用内容推荐逻辑：需求锚点或可信需求假设 -> 内容里的具体判断 -> 对用户的具体帮助 -> 背后的趋势或重要性 -> 明确推荐。",
      "旧激活原话中‘最顺手的本事’‘一句话就行’‘卡片在下面’‘点开先看这一条就行’已被老黄判定 AI 味重，已经撤下，不得引用、改写或模仿。",
      "低信息用户：必须明确写‘我猜你可能’或‘如果你也’，再用内容事实支撑需求假设；不假装熟悉。",
      "有真实课题的用户：自然引用已确认的非敏感需求，再说明这次内容为什么值得看；不得为了个性化硬关联。",
      "只说有帮助、值得看或有启发不算价值说明；必须讲清帮助用户看清什么、避免什么，或背后反映什么趋势。",
      "卡片动作只出现一次，并自然融入最后一句；不要先说一遍操作，再用另一种说法重复。",
    ] if scene == "activation_recall" else [
      "周复盘有效结构：这周给你留了什么 -> 内容为什么值得看 -> 点开卡片查看完整建议。不汇报行为，不要求回复。",
      "内容服务有效样本：有真实近期课题时，自然提到用户原话 -> 说清这篇内容的一个具体看点 -> 普通收尾。没有真实课题时，直接讲内容价值，不硬凑熟悉感，不强行提问。",
      "个性化推送有效样本：只在有证据时引用用户真实课题，如获客系统或内容资产化，再说明这篇日课里哪个判断值得看；隐私信息不用作主动开场，末尾不索取回复。",
      "成交辅助有效样本：先用具体事实证明‘我看见你’，再回到用户为什么问这个问题；优惠/价格只是表层，背后通常是是否适合、是否确定、决策风险。",
      "用户改写风格样本：我还记得 -> 我觉得这里特别好 -> 诚实也是你的武器 -> 要不然你再跟我说说你的业务和顾虑 -> 我可以客观、靠谱、负责任地给你点建议。",
      "高意向有效样本：先确认用户为什么认真考虑，再确认适不适合、时间安排或决策卡点。",
      "产品推进有效样本：够格才坚定，不够格就给更轻路径；不要为了成交硬推。",
    ]),
    "reference_template": template["copy"],
  }
  return "\n".join([
    "你是黄小璨 AI 的私域话术生成助手。系统已按本次场景动态装载资源包；以 prompt_runtime、runtime_instructions、runtime_reference_examples 和 card_capability 为本轮优先约束。请只输出 JSON，不要输出 Markdown。",
    "JSON 格式，必须是 json.loads 可以解析的严格 JSON：",
    '{"diagnosis_summary":"","conversation_strategy":"","facts_to_confirm_before_send":[],"risk_flags":[],"profile_update_candidates":[{"field":"","value":"","source":"","confidence":"","requires_human_confirm":true}],"candidates":[{"variant":"safe","push_copy":"","blocked":false,"blocked_reason":"","copy_angle":"","copy_angle_reason":"","signal_snapshot":"","generation_reason":"","intended_effect":"","signal_collection_plan":"","information_capture_mode":"service_only","information_goal":"","profile_update_on_reply":{"field":"","confirmation_required":true},"data_readiness":"clear","required_info_to_collect":[],"operator_note":"","service_match_card_used":{"user_background":"","user_need":"","recommendation_reason":"","specific_help":"","content_highlights":[]},"voice_reference_used":[],"voice_alignment_note":"","anti_ai_rewrite_note":"","sales_stage":"","next_sales_step":"","risk_flags":[],"needs_manual_handoff":false}]}',
    "生成要求：先按 prompt_runtime.user_context_branch 决定个性化深度，再按 angle_plan 决定本条的表达角度；主动服务场景必须逐项读取 service_match_card，并在 service_match_card_used 里原样记录本次真正采用的背景、需求、推荐理由、具体帮助和内容亮点，但 push_copy 不得按字段逐项复述；再输出 diagnosis_summary 和 conversation_strategy；只使用输入中可追溯事实；企微私信口吻，自然、具体、少 AI 味；只保留一个 CTA；push_copy 里不能出现任何花括号占位符；必须完成原话对照和强去 AI 味重写，并填写 voice_reference_used、voice_alignment_note、anti_ai_rewrite_note；voice_reference_used 必须填写实际参考的 example id；批量场景不能只换称呼和标题，必须体现 angle_plan 的差异；成交场景必须填写 sales_stage 和 next_sales_step；sales_stage_bundle 必须严格遵守其 variant 数量和命名；某类话术当前不适合发送时，设置 blocked=true、push_copy 为空，并把原因写入 blocked_reason 和 operator_note；不要复刻 reference_template 或参考样本的原句；不要在 push_copy 里写内部阶段术语；字段内容里不要使用英文双引号，如需引用请用中文引号「」。",
    "输入：",
    json.dumps(payload, ensure_ascii=False, indent=2),
  ])

def parse_copy_response(text: str) -> dict | None:
  raw = (text or "").strip()
  raw = re.sub(r"^```(?:json)?\s*", "", raw)
  raw = re.sub(r"\s*```$", "", raw)
  try:
    payload = json.loads(raw)
  except Exception:
    start = raw.find("{")
    end = raw.rfind("}")
    if start < 0 or end <= start:
      return None
    try:
      payload = json.loads(raw[start:end + 1])
    except Exception:
      return None
  if not isinstance(payload, dict) or not isinstance(payload.get("candidates"), list):
    return None
  return payload

def strategy_meta_from_parsed(parsed: dict, data: dict) -> dict:
  fallback = template_strategy_meta(data)
  facts = parsed.get("facts_to_confirm_before_send")
  risks = parsed.get("risk_flags")
  updates = parsed.get("profile_update_candidates")
  return {
    "diagnosis_summary": str(parsed.get("diagnosis_summary") or fallback["diagnosis_summary"]).strip(),
    "conversation_strategy": str(parsed.get("conversation_strategy") or fallback["conversation_strategy"]).strip(),
    "facts_to_confirm_before_send": facts if isinstance(facts, list) else fallback["facts_to_confirm_before_send"],
    "risk_flags": risks if isinstance(risks, list) else fallback["risk_flags"],
    "profile_update_candidates": updates if isinstance(updates, list) else [],
  }

def deterministic_copy_repairs(push_copy: str, data: dict) -> tuple[str, list[str]]:
  value = str(push_copy or "").strip()
  repairs: list[str] = []
  if not value:
    return value, repairs
  scene = str(data.get("scene") or "")
  display_name = clean_display_name(data.get("display_name") or "")
  if scene in {"weekly_review", "content_service", "activation_recall"} and display_name and not value.startswith(display_name):
    value = f"{display_name}，{value.lstrip()}"
    repairs.append("prepend_clean_display_name")
  if scene in {"weekly_review", "content_service", "activation_recall"} and display_name:
    alias_match = re.match(rf"^{re.escape(display_name)}[，,]\s*([^，,\n。！？]{{1,12}})[，,]", value)
    alias = alias_match.group(1).strip() if alias_match else ""
    normal_prefixes = ("这周", "这次", "最近", "今天", "刚刚", "上次", "我", "跟你", "黄小璨", "小程序", "给你", "看到", "想跟你", "有个", "有件", "行业")
    if alias and not alias.startswith(normal_prefixes) and re.fullmatch(r"[A-Za-z\u4e00-\u9fff][A-Za-z0-9_\u4e00-\u9fff-]{0,15}", alias):
      value = re.sub(
        rf"^{re.escape(display_name)}[，,]\s*{re.escape(alias)}[，,]\s*",
        f"{display_name}，",
        value,
        count=1,
      )
      repairs.append("remove_duplicate_salutation")
  repaired = re.sub(r"如果你觉得(?:这个)?方向(?:挺|很|可能|应该)?对得上", "如果你觉得这个方向适合你", value)
  if repaired != value:
    value = repaired
    repairs.append("replace_sales_direction_match_phrase")
  repaired = value.replace("挺有意思", "值得留意").replace("适配度", "是不是合适")
  if repaired != value:
    value = repaired
    repairs.append("replace_ai_sounding_word")
  repaired = re.sub(r"(?:挺|很|可能|应该)?对得上(?:你的情况|你现在的事|你现在做的事)?", "可能有些参考", value)
  if repaired != value:
    value = repaired
    repairs.append("replace_ai_sounding_match_phrase")
  if scene in {"personal_followup", "sales_assist", "sales_stage_bundle"}:
    if not str(data.get("entry") or data.get("card_path_or_link") or "").strip():
      repaired = re.sub(r"(?:卡片在下面|我把卡片放下面了?)[^。！\n]*[。！]?\s*", "", value)
      if repaired != value:
        value = repaired.strip()
        repairs.append("remove_invented_sales_card_entry")
    repaired = re.sub(r"(你(?:现在|目前)的客户群体)是什么样的[？?]\s*(?:他们|这些客户)(更[^？?\n]{1,80}[？?])", r"\1\2", value)
    if repaired != value:
      value = repaired
      repairs.append("collapse_sales_customer_question")
    repaired = re.sub(r"(你担心的[^？?\n]{1,50})具体是指什么[？?]\s*是", r"\1具体是指哪一种：", value)
    if repaired != value:
      value = repaired
      repairs.append("collapse_sales_meaning_question")
    repaired = re.sub(r"(最想(?:通过|先)?[^？?\n]{0,30}(?:解决|确认)[^？?\n]{0,16})[？?]\s*(?:是|还是)", r"\1：", value)
    if repaired != value:
      value = repaired
      repairs.append("collapse_sales_option_question")
  try:
    slot = int(data.get("diversity_slot") or data.get("batch_index") or 0)
  except (TypeError, ValueError):
    slot = 0
  if scene == "weekly_review":
    weekly_endings = [
      "完整内容在本周复盘里。",
      "点开复盘卡片，可以看完整分析。",
      "这个判断在复盘里有完整展开。",
      "完整复盘已经放在卡片里。",
      "点开卡片，可以看这次复盘的完整内容。",
    ]
    repaired = re.sub(
      r"(?:卡片在下面[^。！\n]*[。！]?|完整理由和下一步[^。！\n]*[。！]?|你(?:有空|方便的时候|想看的时候)[^。！\n]*[。！]?)\s*$",
      weekly_endings[slot % len(weekly_endings)],
      value,
    )
    if repaired != value:
      value = repaired
      repairs.append("replace_canned_weekly_ending")
    repaired = re.sub(
      r"完整复盘(?:已经)?(?:放|在)[^。！\n]*[。！]?\s*完整内容在本周复盘里。?$",
      weekly_endings[slot % len(weekly_endings)],
      value,
    )
    if repaired != value:
      value = repaired
      repairs.append("deduplicate_weekly_ending")
    review_scene = str(data.get("review_scene") or "")
    if review_scene == "review_action":
      natural_ending = [
        "我把这个判断和后面的建议都放在复盘里了，点击下面的卡片去看看吧。",
        "关于这一步为什么重要，复盘里还有更完整的建议，点击下面的卡片去看看吧。",
        "其他判断和建议，都放在下面的复盘里了。",
      ][slot % 3]
    else:
      natural_ending = [
        "完整内容和这周的学习建议，都在下面的复盘卡片里了。",
        "这篇内容为什么值得看，复盘里有更完整的说明，点击下面的卡片去看看吧。",
        "完整的总结和学习建议，都在下面的复盘里了。",
      ][slot % 3]
    repaired = re.sub(
      r"(?:完整复盘在(?:小程序里|下面|下面卡片里|卡片里)[^。！\n]*[。！]?|完整复盘和为什么[^。！\n]*[。！]?|点开复盘卡片[^。！\n]*[。！]?)\s*$",
      natural_ending,
      value,
    )
    if repaired != value:
      value = repaired
      repairs.append("replace_system_weekly_ending")
    repaired = re.sub(
      r"完整复盘和建议都在下面的卡片里[，,]?\s*这个判断在复盘里有完整展开[。！]?\s*$",
      natural_ending,
      value,
    )
    if repaired != value:
      value = repaired
      repairs.append("replace_repetitive_weekly_ending")
    duplicate_prone_endings = [
      "完整总结和这周的学习建议，都在下面的复盘卡片里了，点开就能看到。",
      "我把完整总结和这周的建议都放进复盘了，点开下面的卡片去看看吧。",
      "这周完整的总结和学习建议都在复盘里，点击下面的卡片可以直接查看。",
      "其他判断和建议也整理在复盘里了，点开下面的卡片去看看吧。",
      "完整复盘已经整理好了，点击下面的卡片可以直接查看。",
    ]
    repaired = re.sub(
      r"完整(?:的)?总结和这周的学习建议[，,]?都在下面的复盘卡片里(?:了)?[，,]?(?:点击|点开)就能看到[。！]?$",
      duplicate_prone_endings[slot % len(duplicate_prone_endings)],
      value,
    )
    if repaired != value:
      value = repaired
      repairs.append("vary_duplicate_prone_weekly_ending")
  if scene == "content_service":
    content_type = str(data.get("content_type") or "")
    content_ending = "完整案例在卡片里。" if content_type == "case" else "完整内容在卡片里。"
    repaired = re.sub(
      r"(?:卡片(?:我)?(?:放)?在下面|卡片(?:我)?放下面了?|卡片给你放下面了?)(?:，?你有空(?:看看|翻翻|看一眼))?[。！]?\s*$",
      content_ending,
      value,
    )
    if repaired != value:
      value = repaired
      repairs.append("replace_weak_content_service_ending")
    varied_endings = [
      "具体内容都在下面这张卡片里了。",
      "文章放在下面了，推荐你看一看。",
      "完整内容在卡片里，发给你。",
      "相关案例和拆解都在卡片里。",
      "下方卡片是完整内容，分享给你。",
    ]
    repaired = re.sub(
      r"(?:完整内容在卡片里[，,]?\s*分享给你|完整内容在卡片里[，,]?\s*发给你|具体内容在卡片里[，,]?\s*分享给你)[。！]?\s*$",
      varied_endings[slot % len(varied_endings)],
      value,
    )
    if repaired != value:
      value = repaired
      repairs.append("vary_content_service_ending")
    repaired = re.sub(
      r"(?:点开卡片|打开卡片)[^。！\n]*(?:再点|然后点)[^。！\n]*(?:深入聊聊|继续聊|继续交流)[^。！\n]*[。！]?\s*$",
      "完整内容和具体拆解都在卡片里，点开去看看吧。",
      value,
    )
    if repaired != value:
      value = repaired
      repairs.append("collapse_content_service_multi_step_cta")
  if scene == "activation_recall":
    content_type = str(data.get("content_type") or "").lower()
    activation_endings = ([
      "完整案例和具体拆解都在卡片里，点开去看看吧。",
      "这个案例后面是怎么做的，点开卡片就能看到。",
      "案例的完整过程放在卡片里了，点击可以直接查看。",
      "具体做法都在案例卡片里，点开去看一下吧。",
      "完整案例在卡片里，打开可以看到后面的拆解。",
    ] if content_type == "case" else ([
      "完整观察和老黄的具体判断都在卡片里，点开去看看吧。",
      "这个趋势后面怎么判断，点开卡片就能看到。",
      "完整分析放在卡片里了，点击可以直接查看。",
      "老黄对这件事的完整判断在卡片里，点开去看一下吧。",
      "完整观察在卡片里，打开可以看到具体分析。",
    ] if content_type == "observation" else [
      "这篇日课和完整判断都在卡片里，点开去读一下吧。",
      "这个问题在日课里还有更完整的展开，点开卡片就能看到。",
      "完整内容放在卡片里了，点击可以直接查看。",
      "这篇内容后面还有具体建议，点开卡片去看一下吧。",
      "完整日课在卡片里，打开可以看到具体分析。",
    ]))
    repaired = re.sub(
      r"(?:具体判断我放在卡片里了，?你有空看看[。！]?|(?:我把|已经把)?卡片(?:一起)?发你了(?:，?里面[^。！\n]*)?[。！]?|卡片(?:我)?放在下面了?，?你有空看看[。！]?)\s*$",
      activation_endings[slot % len(activation_endings)],
      value,
    )
    if repaired != value:
      value = repaired
      repairs.append("replace_bare_activation_ending")
    repaired = re.sub(
      r"(?:具体判断和完整案例|具体内容|具体判断|具体拆解|完整分析)[^\n。！]*(?:放在|都放在|在)卡片[^\n。！]*[。！]?\s*点开[^。！\n]*(?:选|挑)[^。！\n]*(?:再点|我想聊聊这一条)[^。！\n]*[。！]?$",
      activation_endings[slot % len(activation_endings)],
      value,
    )
    if repaired != value:
      value = repaired
      repairs.append("collapse_multi_step_activation_cta")
    if not re.search(r"推荐|建议|值得.{0,4}看|可以看看|我想发你|我把.{0,12}发你|挺适合你|我觉得.{0,24}(?:对你有用|适合你|你会用得上|值得|有启发|有帮助)", value):
      if display_name and value.startswith(f"{display_name}，"):
        body = value[len(display_name) + 1:].lstrip()
        body = re.sub(r"^这周黄小璨(?:\s*AI)?更新了一篇内容[，,]?", "", body)
        body = re.sub(r"^这周有个案例[，,]?(?:讲的是)?", "这个案例讲的是", body)
        value = f"{display_name}，这次有一篇内容，我想推荐给你。\n\n{body}"
      else:
        value = f"这次有一篇内容，我想推荐给你。\n\n{value}"
      repairs.append("add_explicit_activation_recommendation")
    has_concrete_action = bool(re.search(
      r"(?:点开|打开|点击|进入).{0,24}(?:卡片|完整内容|完整观察|具体判断|具体分析|完整案例|完整复盘)|"
      r"(?:卡片|这篇日课|完整观察|完整案例).{0,24}(?:点开|打开|点击|进入)",
      value,
    ))
    if str(data.get("entry") or data.get("card_path_or_link") or "").strip() and not has_concrete_action:
      repaired = re.sub(r"[^。！\n]*卡片[^。！\n]*[。！]?\s*$", activation_endings[slot % len(activation_endings)], value)
      if repaired == value:
        repaired = f"{value.rstrip()}\n\n{activation_endings[slot % len(activation_endings)]}"
      value = repaired
      repairs.append("add_concrete_activation_card_action")
  return value, repairs

def candidates_from_parsed(parsed: dict, data: dict) -> tuple[list[dict], list[dict]]:
  candidates = []
  rejected_quality = []
  runtime_bundle = build_prompt_runtime_bundle(data)
  runtime_reference_ids = [
    str(item.get("id") or "").strip()
    for item in runtime_bundle.get("selected_examples") or []
    if str(item.get("id") or "").strip()
  ]
  voice_reference_fallback = compact_text_list(
    data.get("voice_reference_samples")
    or data.get("style_reference_samples")
    or runtime_reference_ids
    or data.get("historical_success_patterns"),
    limit=5,
  )
  sales_stage_fallback = str(data.get("sales_stage") or "").strip()
  for idx, item in enumerate(parsed.get("candidates") or []):
    if not isinstance(item, dict):
      continue
    push_copy = fill_copy_placeholders(str(item.get("push_copy") or "").strip(), data)
    blocked = bool(item.get("blocked")) or bool(re.match(r"^暂不建议发送", push_copy))
    blocked_reason = str(item.get("blocked_reason") or (push_copy if blocked else "")).strip()
    if blocked:
      push_copy = ""
    risk_flags = item.get("risk_flags") if isinstance(item.get("risk_flags"), list) else []
    scene = str(data.get("scene") or "")
    deterministic_repairs = []
    if not blocked:
      push_copy, deterministic_repairs = deterministic_copy_repairs(push_copy, data)
    candidate_variant = str(item.get("variant") or ("safe" if idx == 0 else "warm"))
    quality_context = {**data, "_candidate_variant": candidate_variant}
    quality_issues = [] if blocked else copy_quality_issues(push_copy, scene, quality_context)
    risk_flags = list(dict.fromkeys([*risk_flags, *quality_issues]))
    candidate = {
      "variant": candidate_variant,
      "push_copy": push_copy,
      "blocked": blocked,
      "blocked_reason": blocked_reason,
      "copy_angle": str(item.get("copy_angle") or data.get("copy_angle") or "").strip(),
      "copy_angle_reason": str(item.get("copy_angle_reason") or data.get("copy_angle_reason") or "").strip(),
      "signal_snapshot": str(item.get("signal_snapshot") or data.get("signal_snapshot") or "").strip(),
      "generation_reason": str(item.get("generation_reason") or "").strip(),
      "intended_effect": str(item.get("intended_effect") or "").strip(),
      "signal_collection_plan": str(item.get("signal_collection_plan") or data.get("signal_collection_plan") or "").strip(),
      "data_readiness": str(item.get("data_readiness") or data.get("data_readiness") or "").strip(),
      "required_info_to_collect": item.get("required_info_to_collect") if isinstance(item.get("required_info_to_collect"), list) else compact_text_list(data.get("required_info_to_collect") or data.get("data_gaps") or data.get("unknowns")),
      "operator_note": str(item.get("operator_note") or "").strip(),
      "service_match_card_used": item.get("service_match_card_used") if isinstance(item.get("service_match_card_used"), dict) else (runtime_bundle.get("service_match_card") or {}),
      "voice_reference_used": item.get("voice_reference_used") if isinstance(item.get("voice_reference_used"), list) else voice_reference_fallback,
      "voice_alignment_note": str(item.get("voice_alignment_note") or ("模型未单独填写风格说明；系统已按本阶段原话锚点兜底标注，需人工对照审核。" if voice_reference_fallback else "")).strip(),
      "anti_ai_rewrite_note": "；".join(filter(None, [
        str(item.get("anti_ai_rewrite_note") or "已通过基础质量闸；仍需人工检查是否像老黄/黄小璨真实表达。").strip(),
        f"确定性修复：{' / '.join(deterministic_repairs)}" if deterministic_repairs else "",
      ])),
      "sales_stage": str(item.get("sales_stage") or sales_stage_fallback).strip(),
      "next_sales_step": str(item.get("next_sales_step") or "").strip(),
      "risk_flags": risk_flags,
      "needs_manual_handoff": bool(item.get("needs_manual_handoff")),
    }
    if (push_copy or blocked) and not (SEVERE_COPY_QUALITY_ISSUES & set(quality_issues)):
      candidates.append(candidate)
    elif push_copy or blocked:
      rejected_quality.append(candidate)
  return candidates, rejected_quality

def enforce_sales_bundle_shape(candidates: list[dict], data: dict) -> tuple[list[dict], list[dict]]:
  if str(data.get("scene") or "") != "sales_stage_bundle":
    return candidates, []
  readiness = str(data.get("data_readiness") or "partial")
  rejected = []
  if readiness == "insufficient":
    if not candidates:
      return [], []
    diagnosis_source = next((item for item in candidates if item.get("variant") == "diagnosis_confirm"), candidates[0])
    diagnosis = {**diagnosis_source, "variant": "diagnosis_confirm"}
    for item in candidates:
      if item is not diagnosis_source:
        rejected.append({**item, "risk_flags": [*item.get("risk_flags", []), "blocked_by_insufficient_profile"]})
    return [diagnosis], rejected

  aliases = {
    "diagnosis_confirm": {"diagnosis_confirm", "diagnosis", "need_confirm", "profile_trust"},
    "product_intro": {"product_intro", "product", "solution", "product_push"},
    "closing": {"closing", "close", "final_close", "objection"},
  }
  variants = [str(item.get("variant") or "") for item in candidates]
  if len(candidates) == 3 and len(set(variants)) == 1 and variants[0] in {"", "safe", "warm", "candidate"}:
    return [
      {**candidates[0], "variant": "diagnosis_confirm"},
      {**candidates[1], "variant": "product_intro"},
      {**candidates[2], "variant": "closing"},
    ], []
  ordered = []
  used = set()
  for expected, accepted in aliases.items():
    match = next((item for item in candidates if id(item) not in used and str(item.get("variant") or "") in accepted), None)
    if not match:
      rejected.extend({**item, "risk_flags": [*item.get("risk_flags", []), f"missing_sales_bundle_variant:{expected}"]} for item in candidates)
      return [], rejected
    used.add(id(match))
    ordered.append({**match, "variant": expected})
  return ordered, rejected

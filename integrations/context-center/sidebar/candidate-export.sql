
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout='60s';
SET LOCAL timezone='Asia/Shanghai';
SELECT json_build_object(
 'contract','crm-production-read-facts.v1','complete',true,'read_only',true,
 'message_scope_version','crm-private-message-scope.v1',
 'source','CRM production read snapshot','source_as_of',transaction_timestamp(),
 'source_snapshot_id','crm-production-'||to_char(transaction_timestamp(),'YYYYMMDDHH24MISS'),
 'customers',(select json_agg(x) from (select id,status,merged_into_customer_id,updated_at from customers order by id)x),
 'users',(select json_agg(x) from (select id customer_id,status customer_status,'{}'::json data_quality from customers order by id)x),
 'identities',(select coalesce(json_agg(x),'[]') from (select customer_id,kind,normalized_value from customer_identities
    where status='active' and (kind='wecom_external_userid' or
      (kind='unionid' and assurance='verified' and scope_key='wechat-open-platform:wx0ca836834b18e989'))
    order by customer_id,id)x),
 'directory_names',(select coalesce(json_object_agg(customer_id,display_name),'{}') from customer_directory_projection),
 'wecom_names',(select coalesce(json_object_agg(customer_id,display_name),'{}') from wecom_external_contact_profiles),
 'wecom_first_followed',(select coalesce(json_object_agg(customer_id,first_followed_at),'{}') from
   (select customer_id,min(followed_at) first_followed_at from wecom_customer_owner_observations
    where relationship_status='active' and followed_at is not null group by customer_id)x),
 'orders',(select coalesce(json_agg(x),'[]') from (
    select o.id order_id,coalesce(o.beneficiary_customer_id,o.payer_customer_id) customer_id,
      o.payer_customer_id,o.beneficiary_customer_id,o.merchant_order_no,o.provider,o.source_system,
      o.amount_minor,o.refunded_minor,o.currency,o.status,o.effect_eligible,o.created_at,o.updated_at,
      json_build_object('order_items',(select coalesce(json_agg(i),'[]') from
        (select line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor,product_version
         from order_items where order_id=o.id order by line_no)i)) payload
    from orders o order by o.id)x),
 'messages',(select coalesce(json_agg(x),'[]') from (
    select distinct p.customer_id_at_ingest customer_id,m.id message_id,m.occurred_at,m.content_text,m.msgtype,
      p.participant_role,m.conversation_type,m.corp_scope,
      coalesce((select jsonb_agg(staff.provider_value order by staff.provider_value) from
        (select distinct provider_value from message_archive_participants
         where message_id=m.id and actor_type='staff' and m.conversation_type='private')staff),'[]'::jsonb) staff_provider_user_ids,
      (select count(*) from message_archive_participants where message_id=m.id) participant_count,
      (select case when count(*)=1 then min(actor_type) else 'ambiguous' end
         from message_archive_participants where message_id=m.id and participant_role='sender') sender_actor_type
    from message_archive_participants p join message_archive_messages m on m.id=p.message_id
    where p.customer_id_at_ingest is not null and p.actor_type='external_customer'
      and (p.participant_role='sender' or (m.conversation_type='private' and p.participant_role='recipient'))
    order by p.customer_id_at_ingest,m.occurred_at desc)x),
 'surveys',(select coalesce(json_agg(x),'[]') from (
    with legacy as (
      select p.submission_id,min(i.customer_id) customer_id from survey_legacy_external_projections p
      join customer_identities i on i.kind='unionid' and i.status='active' and i.assurance='verified'
        and i.scope_key='wechat-open-platform:wx0ca836834b18e989' and i.normalized_value=p.historical_unionid
      group by p.submission_id having count(distinct i.customer_id)=1
    ), phone as (
      select submission_id,min(customer_id) customer_id from survey_phone_binding_receipts
      where customer_id is not null and status in ('confirmed','active','resolved','bound')
      group by submission_id having count(distinct customer_id)=1
    )
    select s.id submission_id,coalesce(s.customer_id,l.customer_id,p.customer_id) customer_id,
      case when s.customer_id is not null then 'source_customer_id' when l.customer_id is not null then 'legacy_unionid'
        when p.customer_id is not null then 'phone_binding' else 'unresolved' end resolution_method,
      s.submitted_at,s.title_snapshot title,s.questionnaire_slug_snapshot,s.source_channel,s.campaign_id,
      s.identity_state source_identity_state,s.identity_reason source_identity_reason,
      case when l.customer_id is not null then 'unique_verified_unionid_same_scope'
        when s.customer_id is not null then 'source_customer_id'
        when p.customer_id is not null then 'confirmed_phone_binding_receipt' else 'unresolved' end resolution_evidence
      from survey_submissions s
    left join legacy l on l.submission_id=s.id left join phone p on p.submission_id=s.id order by s.id)x),
 'answers',(select coalesce(json_agg(x),'[]') from (select id answer_id,submission_id,
    question_title_snapshot question_title,text_value_masked,selected_options_snapshot selected_options
    from survey_submission_answers order by submission_id,id)x),
 'activation_receipts',(select coalesce(json_agg(x),'[]') from (
    select r.customer_id,r.occurred_at,c.code channel_code,v.name channel_name
    from channel_acquisition_entrant_receipts r
    join channel_acquisition_state_bindings b on b.id=r.binding_id
    join channels c on c.id=b.channel_id
    left join channel_config_versions v on v.channel_id=c.id and v.config_version=c.current_config_version
    where r.customer_id is not null and r.occurred_at >= transaction_timestamp()-interval '187 days'
    order by r.occurred_at)x),
 'hxc_identity_resolution',(select json_build_object('complete',true,'source_as_of',transaction_timestamp(),
    'by_account_digest',coalesce(json_object_agg(a.account_digest,json_build_object(
      'status',coalesce(s.status,'pending'),'customer_id',s.customer_id,'matched_by',s.matched_by,
      'reason_code',coalesce(s.reason_code,'source_subject_not_seen'),'last_seen_at',s.last_seen_at)),'{}'))
    from (values __ACCOUNT_VALUES__)a(account_digest,subject_digest)
    left join identity_source_subjects s on s.source_system='hxc' and s.subject_digest=decode(a.subject_digest,'hex'))
);
ROLLBACK;

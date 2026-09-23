BEGIN;
-- Synthetic staging-only facts. No production identifiers, phones or Provider data.
INSERT INTO customers(id,status) OVERRIDING SYSTEM VALUE VALUES (990001,'active')
ON CONFLICT (id) DO UPDATE SET status='active', updated_at=clock_timestamp();
INSERT INTO customer_identities(id,customer_id,kind,scope_key,normalized_value,assurance,source,source_event_id,normalizer_version,status,verified_at)
OVERRIDING SYSTEM VALUE VALUES (990002,990001,'wecom_external_userid','wecom-corp:staging-fixture','staging-external-customer-001','verified','staging_fixture','staging-customer-sync-v1',1,'active',clock_timestamp())
ON CONFLICT (id) DO UPDATE SET customer_id=990001,status='active',updated_at=clock_timestamp();
INSERT INTO wecom_customer_sync_runs(run_key,trigger_type,status,corp_scope,staff_ids,discovered_count,activated_count,projected_count,started_at,completed_at)
VALUES ('staging-fixture:customer-sync:v1','manual','succeeded','wecom-corp:staging-fixture','["staging-staff-001"]',1,1,1,clock_timestamp(),clock_timestamp())
ON CONFLICT (run_key) DO UPDATE SET status='succeeded',discovered_count=1,activated_count=1,projected_count=1,completed_at=clock_timestamp()
RETURNING id;
INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,corp_name,oneid_label,activation_status,source,last_synced_at)
VALUES (990001,'active','预发布合成客户','预发布合成企业','staging fixture customer','active','staging_fixture',clock_timestamp())
ON CONFLICT (customer_id) DO UPDATE SET display_name=EXCLUDED.display_name,source='staging_fixture',last_synced_at=clock_timestamp();
INSERT INTO wecom_external_contact_profiles(customer_id,corp_scope,external_identity_id,display_name,corp_name,activation_status,profile_digest,last_seen_run_id,fetched_at)
SELECT 990001,'wecom-corp:staging-fixture',990002,'预发布合成客户','预发布合成企业','active',decode(repeat('a',64),'hex'),id,clock_timestamp()
FROM wecom_customer_sync_runs WHERE run_key='staging-fixture:customer-sync:v1'
ON CONFLICT (customer_id) DO UPDATE SET display_name=EXCLUDED.display_name, last_seen_run_id=EXCLUDED.last_seen_run_id, fetched_at=clock_timestamp();
INSERT INTO media_blobs(digest,mime_type,byte_size,content)
VALUES ('sha256:'||repeat('b',64),'image/png',70,decode('89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d49444154789c6360f8cfc000000301010018dd8db40000000049454e44ae426082','hex'))
ON CONFLICT (digest) DO NOTHING;
INSERT INTO media_images(id,blob_digest,file_name,name,category,mime_type,byte_size,width,height,created_by,updated_by)
OVERRIDING SYSTEM VALUE VALUES (990010,'sha256:'||repeat('b',64),'staging-fixture-lesson.png','预发布合成教研课卡','staging-fixture','image/png',70,1,1,1,1)
ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name,enabled=true,updated_at=clock_timestamp();
INSERT INTO media_miniprograms(id,name,app_id,page_path,title,thumb_image_id,created_by,updated_by)
OVERRIDING SYSTEM VALUE VALUES (990011,'预发布合成日课','staging-fixture-app','pages/article/article?lesson_id=00000000-0000-0000-0000-000000000001&from=learn','预发布合成教研课卡',990010,1,1)
ON CONFLICT (id) DO UPDATE SET title=EXCLUDED.title,thumb_image_id=EXCLUDED.thumb_image_id,enabled=true,updated_at=clock_timestamp();
INSERT INTO media_legacy_material_mappings(source_system,legacy_material_kind,legacy_material_id,material_kind,material_id,source_digest,source_record_digest,imported_by)
VALUES ('staging-fixture','miniprogram','00000000-0000-0000-0000-000000000001','miniprogram',990011,'sha256:'||repeat('c',64),'sha256:'||repeat('d',64),'staging-fixture:v1')
ON CONFLICT (source_system,legacy_material_kind,legacy_material_id) DO NOTHING;
COMMIT;

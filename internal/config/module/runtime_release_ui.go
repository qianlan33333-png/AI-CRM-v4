package module

// runtimeReleaseHostTemplate is the Config-owned V3 host counterpart for the
// frozen release list/new/detail interaction in the dd8 donor. Its browser
// adapter supplies only the closed runtime catalog and server-issued action
// proofs; old AdminOps DTOs and controllers remain untouched.
const runtimeReleaseHostTemplate = `<section class="admin-card"><div class="admin-card-head"><div><h2>配置发布</h2><p>正在读取发布记录。</p></div></div></section>`

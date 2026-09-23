# PR09 frontend donor archive

The files under `src/` are byte-exact snapshots from the frozen v2 donor
commit `6bfbe5816bb89913c70adaca87d6a486260e016e`. They are archive evidence,
not active v3 frontend code, and must not be edited.

The archive contains the original configuration/config-detail/API-docs
templates, setup-wizard behavior, generated configuration/Admin Ops/release
request contracts, and generated health/diagnostic contracts. Some generated
artifacts necessarily contain adjacent legacy operations or shared schema
declarations; those operations are explicitly out of scope and must remain
unmounted. Terra/Web must narrow imports before wiring any page.

The only eventual shell is PR10's v3-owned
`internal/webshell/templates/admin_base.html` and its primary sidebar. Mount
the selected donor business hooks through a v3-owned adapter. Do not deploy a
complete v2 page, copy a second `.side` navigation, or change donor wording,
CSS, interaction, or request-contract bytes.

# PR04 Product slice

This package is the bounded Product-domain preparation for PR04. It keeps the
local behavior needed to define standard products and service-period products:
images, price, currency, stock, local lifecycle, publish/unpublish state,
admin tags in the compatibility projection, and external-push configuration.

The package deliberately does not own or implement orders, payments, refunds,
entitlements, memberships, member grids, customers, or OneID. It has no v2
runtime, database, module, migration, or provider dependency. Product image
references remain opaque values; the eventual integration must use the
Media-owned port rather than a cross-domain table read.

`port/events.go` is a small v3-local event seam used to preserve the donor
application behavior in isolation. Terra must adapt it to the v3 versioned
event port and transaction/outbox implementation when the composition root and
Product store are integrated. The Product external-push application records
only local configuration and accepted/queued facts; Provider execution and
reconciliation remain outside this slice.

The raw frontend donor files are staged under
`web/donors/products-v2/src/`. They are a byte-exact, non-build staging copy
for the Web integration lane and must not be edited in this preparation PR.

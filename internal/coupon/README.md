# Coupon

Coupon owns rule definitions, customer claims, and the local checkout lifecycle:
claim, reserve, consume and authoritative-close release. It freezes a product
type/ID/code and price snapshot before an Order is created. The Order and
Payment integration supplies the trusted holder, payment result and enclosing
PostgreSQL transaction; this package neither resolves identities nor calls a
payment provider.

`port/target.go` is a narrow compatibility port for checking a Product
target's price/currency before publication. The composition root adapts it to
the canonical Product port; Coupon must not import Product app/store/http
packages or read Product tables.

Claim, reservation and redemption facts each write their receipt, audit entry
and local outbox in the caller's transaction. An unknown payment outcome does
not release a reservation. A close after expiry marks the claim expired.

The raw frontend donor is staged under
`web/donors/coupons-v2/src/`. Those files are immutable byte-exact evidence,
outside the build tree, and must not be edited. The coupon data page is adapted
through Coupon-owned rule and claim-read ports; it never exposes channel
identifiers or payment-provider data.

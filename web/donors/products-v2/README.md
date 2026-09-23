# PR04 raw frontend donor

The files below are copied byte-for-byte from the frozen v2 donor commit and
are staged outside `web/src` so this preparation commit cannot accidentally
build or wire the donor frontend. Do not edit these files here. The Web lane
must select the `products` and `spProducts` templates and their required
shared/generated dependencies, while removing excluded order, payment,
entitlement, membership, member-grid, customer, and identity branches before
integration. `admin/sections/util.ts` and `admin/sections/qr.ts` are included
because the product share modal imports them; the latter's `qrcode-generator`
import is donor evidence only and must be replaced or provided by a v3-owned
adapter without changing dependency locks.

# PR05 raw frontend donor

The files below are copied byte-for-byte from the frozen v2 donor commit and
are staged outside `web/src` so this preparation commit cannot build or wire
the donor frontend. Do not edit them. The raw `couponData` template and shared
admin controller contain references to claim/statistics views; those
excluded-domain routes must remain unavailable or feature-gated on the
backend, and no one may alter this donor copy to hide or rewrite them.

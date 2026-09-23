# HXC unregistered audience: native negative evidence

Scope: source package 28's business concept is an owner-scoped WeCom contact that is not registered in HXC. This is different from an empty directory phone field. Only rule calculation is in scope; no old member list, new identity matcher, or outbound effect.

OneID: read canonical customer and reuse Identity HXC inspection. Persistence: immutable HXC projection and a read-only evidence port pinned to one version; no independent business table writes or Provider mutation.

## Existing facts

- `customer/port.AudienceRegistrationReader` represents directory phone presence. It does not answer HXC registration.
- `hxcdashboard/port.VersionedSharedFactsReader` returns canonical `matched` rows from an immutable HXC projection. Existing rows have `Registered=true`; absence remains unknown, as documented by its availability contract.
- `identity/port.HXCIdentityInspector` inspects source subjects through the existing scope-aware UnionID and phone logic; it returns matched/unmatched/conflict/invalid. It does not provide a reverse exhaustive absence proof for a supplied customer.
- Source scope missing, duplicate source keys, competing roots and unmatched rows are already explicit inspection outcomes. They must not become unregistered facts.

## Minimal viable native extension

Add an HXC-owned versioned registration Port accepting canonical customer IDs, returning `registered`, `unregistered`, or `unknown` plus projection version, source timestamp and completeness status. Keep directory-registration semantics unchanged. Segment's HXC-specific rule composes that Port with the existing WeCom contact/Access owner Ports.

A matched row in a complete, current published source generation establishes registered. A missing row may establish unregistered only when that same generation has an exhaustive source-completeness receipt **and** every retained source subject has completed unambiguous Identity classification, so unresolved source rows cannot hide an alternative association. Initially use a conservative global gate: any unmatched/conflict/invalid row disables negative results for missing candidates. All batches pin the same projection version and completeness receipt. Candidate key eligibility/unknown identity state is read through Identity rather than directly accessing its tables.

This conservative initial gate is implementable without a second matcher but will yield unknown whenever source identity coverage is incomplete. To emit useful negatives while unrelated source rows remain unresolved requires an Identity-owned reverse exclusion proof: for each candidate's allowed scoped identities, check a complete HXC source-identity index through Identity; return no-match only when every relevant key was actually compared under the validated scope. HXC/Segment must not rebuild mobile hashes, export identifiers, or join raw phone/UnionID values themselves. Index publication, classification generation, and HXC source publication must bind atomically to an immutable snapshot receipt; network reads stay outside the transaction.

## Completion checks

1. One complete snapshot, uniquely matched subject: registered.
2. Missing canonical customer with complete source/classification and eligible identity evidence: unregistered.
3. Incomplete source, old generation, missing candidate identity, unmatched source subject, duplicate key, cross-root conflict or source read failure: unknown, never unregistered.
4. Batch reads remain pinned when a new source publication appears midway.
5. Owner restriction excludes other staff even when their customer is a proven negative.
6. No production traffic or customer write is needed for these tests; real native preview must additionally report unknown coverage rather than merely HTTP 200.

No negative-evidence implementation has been claimed complete here. The current ports cannot prove package 28's HXC non-registration by inspecting absence alone. Activating a placeholder that silently treats missing HXC rows or empty CRM phones as unregistered would change the business condition.

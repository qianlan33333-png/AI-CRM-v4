# PR09 release-plane slice

This package freezes local release-candidate attestations, prerequisite and
rollback evidence, cutover journal state, worker generations/fences, and
idempotent lifecycle transitions. It records local facts only: starting a
cutover or requesting rollback does not deploy, switch traffic, invoke backup,
contact WeCom, or claim external success.

The repository and UnitOfWork remain the seams for the full candidate/cutover
plane. `app.ObservationService` is the deliberately smaller composed path for
recording the currently observed local release SHA through the AdminOps safe
projection port. It has no deployer, traffic switch, Provider call, customer
execution, or success claim. Full candidate/cutover persistence and routes
remain unmounted until their owner tables and independent closure are added.

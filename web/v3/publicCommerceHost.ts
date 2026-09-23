export type PublicCommerceMount = {
  dispose(): void;
};

function visible(element: HTMLElement | null): boolean {
  return !!element && !element.hidden;
}

// mountPublicCommerce is intentionally presentation-only. It consumes the
// existing page's explicit hidden regions, completion class, and disabled
// primary action; it never parses Chinese status text, requests a new API, or
// invokes checkout/OAuth/Provider behavior.
export function mountPublicCommerce(root: HTMLElement | null): PublicCommerceMount | null {
  if (!root || root.dataset.publicCommerceMounted === 'true') return null;
  root.dataset.publicCommerceMounted = 'true';

  const refresh = () => {
    const identity = root.querySelector<HTMLElement>('#identityGate');
    const detail = root.querySelector<HTMLElement>('#detailContent');
    const checkout = root.querySelector<HTMLElement>('#checkoutContent');
    root.dataset.publicCommerceView = visible(identity)
      ? 'identity'
      : visible(detail)
        ? 'detail'
        : visible(checkout)
          ? 'checkout'
          : 'neutral';

    const status = root.querySelector<HTMLElement>('#status');
    root.dataset.publicCommerceCompletion = status?.classList.contains('completion-result') ? 'true' : 'false';

    const primary = root.querySelector<HTMLButtonElement>('#buy');
    // The frozen service-period renderer keeps its fixed action bar adjacent
    // to, rather than inside, the decorated main element. It is still the
    // Owner's explicit disabled fact for this one public document.
    const servicePeriodPrimary = root.querySelector<HTMLButtonElement>('#servicePeriodPayButton')
      || root.ownerDocument.querySelector<HTMLButtonElement>('#servicePeriodPayButton');
    root.dataset.publicCommercePrimaryAction = primary?.disabled || servicePeriodPrimary?.disabled ? 'disabled' : 'enabled';
  };

  const onImageError = (event: Event) => {
    const image = event.target;
    if (!(image instanceof HTMLImageElement) || !image.matches('.detail-image,.completion-qr,.slice-img')) return;
    image.dataset.publicMediaState = 'unavailable';
  };

  const observer = new MutationObserver(refresh);
  observer.observe(root.ownerDocument.documentElement, { attributes: true, attributeFilter: ['hidden', 'class', 'disabled'], subtree: true });
  root.addEventListener('error', onImageError, true);
  refresh();

  return {
    dispose() {
      observer.disconnect();
      root.removeEventListener('error', onImageError, true);
      delete root.dataset.publicCommerceMounted;
      delete root.dataset.publicCommerceView;
      delete root.dataset.publicCommerceCompletion;
      delete root.dataset.publicCommercePrimaryAction;
    },
  };
}

if (typeof document !== 'undefined') {
  mountPublicCommerce(document.querySelector<HTMLElement>('[data-v3-public-commerce]'));
}

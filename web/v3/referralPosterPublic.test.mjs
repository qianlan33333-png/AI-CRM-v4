// The public poster journey does not compile unrelated admin sections.
process.env.AICRM_REFERRAL_PUBLIC_ONLY = '1';
await import('./referralPosterShareText.test.mjs');

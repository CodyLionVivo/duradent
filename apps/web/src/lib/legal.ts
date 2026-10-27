// The legal pages and their footer labels. Slugs are the same in every
// language; the content lives in src/content/legal/<locale>/<slug>.md.
export const LEGAL_PAGES = ['terms', 'privacy', 'returns'] as const;
export type LegalPage = (typeof LEGAL_PAGES)[number];

export function isLegalPage(value: string | undefined): value is LegalPage {
  return (LEGAL_PAGES as readonly string[]).includes(value ?? '');
}

/** Tax ID of Duradent S.A.C., shown in the footer and the legal pages. */
export const RUC = '20268534866';

/** Store phone, shown in the footer and the legal pages. */
export const PHONE = { display: '+51 933 741 379', href: 'tel:+51933741379' };

/** Official social accounts of Duradent SAC. */
export const SOCIAL = {
  facebook: 'https://www.facebook.com/duradentsac/',
  instagram: 'https://www.instagram.com/duradent_sac/',
};

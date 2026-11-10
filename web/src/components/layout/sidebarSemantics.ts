/**
 * The sidebar's element and roles. On a phone it is a drawer: a modal dialog, labelled like the
 * navigation it holds (the shell makes it inert while closed). On wider screens it is the page's
 * complementary landmark.
 */
export function sidebarSemantics(isMobile: boolean, label: string) {
  return isMobile
    ? {
        tag: 'div' as const,
        props: { role: 'dialog', 'aria-modal': true, 'aria-label': label },
      }
    : { tag: 'aside' as const, props: {} };
}

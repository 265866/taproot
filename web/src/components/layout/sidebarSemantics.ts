/**
 * The sidebar's roles. On a phone it is a drawer: a modal dialog, labelled like the navigation it
 * holds (the shell makes it inert while closed). On wider screens it is the page's complementary
 * landmark. One element takes either role, so rotating a tablet across the phone breakpoint keeps
 * the sidebar and its focus rather than remounting it.
 */
export function sidebarSemantics(isMobile: boolean, label: string) {
  return isMobile
    ? { role: 'dialog', 'aria-modal': true, 'aria-label': label }
    : { role: 'complementary' };
}

import styles from './Field.module.scss';

/** Pulse highlight for a search jump; useFieldJump adds and removes it. */
export const FIELD_HIGHLIGHT_CLASS: string = styles.fieldHighlightActive;

/**
 * Scope root for the form-control overrides (:global(.input/.form-group/...)). SectionCard's
 * content area applies it; apply it by hand when rendering fields outside a card, such as in a
 * modal.
 */
export const FIELDS_ROOT_CLASS: string = styles.fieldsRoot;

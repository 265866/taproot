import styles from './Field.module.scss';

/** 搜索跳转的脉冲高亮 class（useFieldJump 命令式挂载/移除）。 */
export const FIELD_HIGHLIGHT_CLASS: string = styles.fieldHighlightActive;

/**
 * 表单控件宿主 class：区块编辑器内 :global(.input/.form-group/...) 覆盖的作用域根。
 * SectionCard 的内容区自动挂载；脱离卡片渲染表单块（如 Modal 内容）时手动挂。
 */
export const FIELDS_ROOT_CLASS: string = styles.fieldsRoot;

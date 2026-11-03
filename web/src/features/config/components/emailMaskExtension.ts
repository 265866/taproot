import { RangeSetBuilder } from '@codemirror/state';
import {
  Decoration,
  EditorView,
  ViewPlugin,
  WidgetType,
  type DecorationSet,
  type ViewUpdate,
} from '@codemirror/view';
import { findEmails } from '@/utils/emailMask';

class MaskedText extends WidgetType {
  constructor(readonly text: string) {
    super();
  }

  eq(other: MaskedText): boolean {
    return other.text === this.text;
  }

  toDOM(): HTMLElement {
    const span = document.createElement('span');
    span.textContent = this.text;
    return span;
  }
}

function maskedRanges(view: EditorView): DecorationSet {
  const builder = new RangeSetBuilder<Decoration>();
  for (const { from, to } of view.visibleRanges) {
    for (const match of findEmails(view.state.doc.sliceString(from, to))) {
      builder.add(
        from + match.from,
        from + match.to,
        Decoration.replace({ widget: new MaskedText(match.masked) })
      );
    }
  }
  return builder.finish();
}

/**
 * Draws emails masked without changing the document, so saving, search, and copy keep the real
 * text. The cursor steps over a masked address as one unit; show emails to edit one.
 */
export const maskEmailsInEditor = ViewPlugin.fromClass(
  class {
    decorations: DecorationSet;

    constructor(view: EditorView) {
      this.decorations = maskedRanges(view);
    }

    update(update: ViewUpdate) {
      if (update.docChanged || update.viewportChanged) this.decorations = maskedRanges(update.view);
    }
  },
  {
    decorations: (plugin) => plugin.decorations,
    provide: (plugin) =>
      EditorView.atomicRanges.of((view) => view.plugin(plugin)?.decorations ?? Decoration.none),
  }
);

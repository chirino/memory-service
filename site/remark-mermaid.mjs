import { visit } from 'unist-util-visit';

/**
 * Remark plugin that converts mermaid code blocks into <pre class="mermaid"> elements.
 * This runs before Shiki syntax highlighting, preventing mermaid blocks from being
 * processed as code and instead preserving them for client-side rendering.
 */
export function remarkMermaid() {
  return (tree) => {
    visit(tree, 'code', (node, index, parent) => {
      if (node.lang !== 'mermaid') return;

      const escaped = node.value
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;');

      parent.children[index] = {
        type: 'html',
        value: `<pre class="mermaid">${escaped}</pre>`,
      };
    });
  };
}

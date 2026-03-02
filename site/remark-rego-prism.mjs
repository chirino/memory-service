import Prism from 'prismjs';
import 'prismjs/components/prism-rego.js';
import { visit } from 'unist-util-visit';

/**
 * Remark plugin: converts ```rego code blocks into Prism-highlighted HTML.
 *
 * Astro/Shiki doesn't currently bundle a Rego grammar, so we pre-render only
 * Rego fences and leave every other language to the normal Shiki pipeline.
 */
export function remarkRegoPrism() {
  return (tree) => {
    visit(tree, 'code', (node, index, parent) => {
      if (!parent || typeof index !== 'number' || node.lang !== 'rego') {
        return;
      }

      const html = Prism.highlight(node.value, Prism.languages.rego, 'rego');

      parent.children[index] = {
        type: 'html',
        value: `<pre class="language-rego"><code class="language-rego">${html}</code></pre>`,
      };
    });
  };
}

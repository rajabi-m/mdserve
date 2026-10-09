/* Shared live/static rendering boundary. DOMPurify is bundled locally. */
(function () {
  'use strict';
  const htmlPolicy = {
    USE_PROFILES: {html: true},
    FORBID_TAGS: ['style', 'form', 'button', 'textarea', 'select', 'option'],
    FORBID_ATTR: ['style', 'srcset', 'name'],
    ALLOW_DATA_ATTR: false,
    SANITIZE_NAMED_PROPS: true
  };
  function sanitizeHTML(html) {
    const fragment = DOMPurify.sanitize(html, {...htmlPolicy, RETURN_DOM_FRAGMENT: true});
    // Preserve GFM checkboxes without permitting interactive document forms.
    fragment.querySelectorAll('input').forEach(input => {
      if (input.type !== 'checkbox') input.remove();
      else input.disabled = true;
    });
    return fragment;
  }
  let serial = 0;
  async function renderDiagrams(nodes, theme) {
    mermaid.initialize({
      startOnLoad: false, securityLevel: 'strict', theme,
      htmlLabels: false, flowchart: {htmlLabels: false},
      fontFamily: '-apple-system,Segoe UI,Roboto,sans-serif',
      // Document directives must not weaken the rendering boundary or inject CSS.
      secure: ['secure', 'securityLevel', 'startOnLoad', 'maxTextSize', 'maxEdges',
        'htmlLabels', 'flowchart', 'theme', 'themeCSS', 'themeVariables', 'fontFamily']
    });
    for (const node of nodes) {
      const source = node.textContent;
      try {
        const {svg} = await mermaid.render('mdserve-diagram-' + (++serial), source);
        const fragment = DOMPurify.sanitize(svg, {
          USE_PROFILES: {svg: true, svgFilters: true}, RETURN_DOM_FRAGMENT: true,
          FORBID_TAGS: ['foreignObject', 'a', 'image', 'animate', 'set'],
          ALLOW_DATA_ATTR: false
        });
        node.replaceChildren(fragment);
        // Do not attach Mermaid's document-controlled callbacks.
      } catch (error) {
        node.textContent = source;
        console.warn('mermaid', error);
      }
    }
  }
  window.mdserveSecurity = Object.freeze({sanitizeHTML, renderDiagrams});
})();

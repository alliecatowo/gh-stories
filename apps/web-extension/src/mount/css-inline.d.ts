/** Vite's `?inline` CSS import returns the stylesheet as a plain string
 * instead of injecting a `<style>` tag — used to load `@gh-stories/ui`'s
 * stylesheet into a manually created Shadow DOM root. Not declared by
 * `vite/client.d.ts`, which only covers bare `*.css` imports. */
declare module "*.css?inline" {
  const css: string;
  export default css;
}

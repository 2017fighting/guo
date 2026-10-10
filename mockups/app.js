// 共享：主题切换 + 移动端底部导航
(function () {
  var stored = localStorage.getItem("guo-theme");
  var dark = stored ? stored === "dark" : matchMedia("(prefers-color-scheme: dark)").matches;
  apply(dark);
  window.guoToggleTheme = function () { apply(!(document.documentElement.classList.contains("dark"))); };
  function apply(d) {
    document.documentElement.classList.toggle("dark", d);
    localStorage.setItem("guo-theme", d ? "dark" : "light");
  }
})();

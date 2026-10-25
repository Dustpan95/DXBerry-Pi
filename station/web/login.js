(function () {
  "use strict";
  var form = document.getElementById("login");
  var errorLine = document.getElementById("error");
  var button = form.querySelector("button");

  // Only a path on this host: one leading slash, never "//", "/\" or a scheme.
  function nextPath() {
    var next = new URLSearchParams(window.location.search).get("next");
    if (next && next.charAt(0) === "/" && next.charAt(1) !== "/" && next.charAt(1) !== "\\" && next.indexOf("\n") < 0 && next.indexOf("\r") < 0) {
      return next;
    }
    return "/";
  }

  function show(text) {
    errorLine.textContent = text;
  }

  form.addEventListener("submit", function (ev) {
    ev.preventDefault();
    show("");
    button.disabled = true;
    fetch("/api/auth/login", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        user: document.getElementById("user").value,
        password: document.getElementById("password").value
      })
    }).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (body) {
        if (res.ok) {
          window.location.assign(nextPath());
          return;
        }
        if (res.status === 429) {
          show("Too many attempts. Try again in " + (body.retry_after || 60) + " seconds.");
        } else {
          show(body.error || "Login failed.");
        }
        button.disabled = false;
      });
    }).catch(function () {
      show("Could not reach the Pi.");
      button.disabled = false;
    });
  });
})();

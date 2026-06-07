/* @refresh reload */
import { render } from "solid-js/web";
import { Route, Router } from "@solidjs/router";

import "./index.css";

import SignIn from "@/routes/SignIn";
import SignUp from "@/routes/SignUp";
import SignUpVerify from "@/routes/SignUpVerify";
import ForgotPassword from "@/routes/ForgotPassword";
import ResetPassword from "@/routes/ResetPassword";
import Dashboard from "@/routes/Dashboard";
import AppShell from "@/routes/AppShell";

const root = document.getElementById("root");
if (!root) throw new Error("#root not found");

render(
  () => (
    <Router>
      <Route
        path="/"
        component={() => (
          <AppShell>
            <Dashboard />
          </AppShell>
        )}
      />
      <Route
        path="/dashboard"
        component={() => (
          <AppShell>
            <Dashboard />
          </AppShell>
        )}
      />
      <Route path="/sign-in" component={SignIn} />
      <Route path="/sign-in/forgot" component={ForgotPassword} />
      <Route path="/sign-in/reset" component={ResetPassword} />
      <Route path="/sign-up" component={SignUp} />
      <Route path="/sign-up/verify" component={SignUpVerify} />
    </Router>
  ),
  root,
);

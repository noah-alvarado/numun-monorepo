/* @refresh reload */
import { render } from "solid-js/web";
import { Route, Router } from "@solidjs/router";

import "./index.css";

import { initSentry } from "@/lib/sentry";

initSentry();

import SignIn from "@/routes/SignIn";
import SignUp from "@/routes/SignUp";
import SignUpVerify from "@/routes/SignUpVerify";
import ForgotPassword from "@/routes/ForgotPassword";
import ResetPassword from "@/routes/ResetPassword";
import Dashboard from "@/routes/Dashboard";
import AppShell from "@/routes/AppShell";
import MyDelegation from "@/routes/MyDelegation";
import AdminDelegations from "@/routes/AdminDelegations";
import DelegateImport from "@/routes/DelegateImport";
import DelegateImportPreview from "@/routes/DelegateImportPreview";
import DelegateImportResult from "@/routes/DelegateImportResult";
import Committees from "@/routes/Committees";
import AssignmentStudio from "@/routes/AssignmentStudio";
import AdminEmailHealth from "@/routes/AdminEmailHealth";
import AdminCheckIn from "@/routes/AdminCheckIn";
import Awards from "@/routes/Awards";

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
      <Route
        path="/delegation"
        component={() => (
          <AppShell>
            <MyDelegation />
          </AppShell>
        )}
      />
      <Route
        path="/delegations/:delegationId"
        component={() => (
          <AppShell>
            <MyDelegation />
          </AppShell>
        )}
      />
      <Route
        path="/admin/delegations"
        component={() => (
          <AppShell>
            <AdminDelegations />
          </AppShell>
        )}
      />
      <Route
        path="/delegations/:delegationId/delegates/import"
        component={() => (
          <AppShell>
            <DelegateImport />
          </AppShell>
        )}
      />
      <Route
        path="/delegations/:delegationId/delegates/import/preview"
        component={() => (
          <AppShell>
            <DelegateImportPreview />
          </AppShell>
        )}
      />
      <Route
        path="/delegations/:delegationId/delegates/import/result"
        component={() => (
          <AppShell>
            <DelegateImportResult />
          </AppShell>
        )}
      />
      <Route
        path="/conferences/:conferenceId/committees"
        component={() => (
          <AppShell>
            <Committees />
          </AppShell>
        )}
      />
      <Route
        path="/conferences/:conferenceId/assignments"
        component={() => (
          <AppShell>
            <AssignmentStudio />
          </AppShell>
        )}
      />
      <Route
        path="/admin/email-health"
        component={() => (
          <AppShell>
            <AdminEmailHealth />
          </AppShell>
        )}
      />
      <Route
        path="/admin/check-in"
        component={() => (
          <AppShell>
            <AdminCheckIn />
          </AppShell>
        )}
      />
      <Route
        path="/awards"
        component={() => (
          <AppShell>
            <Awards />
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

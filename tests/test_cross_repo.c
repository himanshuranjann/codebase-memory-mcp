/*
 * test_cross_repo.c — Tests for cross-repo route matching (pass_cross_repo.c).
 *
 * Regression coverage for the handler-dependency defect: cross-repo HTTP_CALLS
 * matches were gated on the target Route having a HANDLES edge to a handler
 * function. Indexed routes frequently have no HANDLES edge (framework/infra
 * registrations), so the matcher emitted zero CROSS_HTTP_CALLS edges across a
 * fully-indexed fleet. A Route node match alone is now authoritative; the
 * handler is optional enrichment used only for the reverse edge.
 */
#include "test_framework.h"
#include "../src/foundation/compat.h"
#include <pipeline/pass_cross_repo.h>
#include <store/store.h>
#include <foundation/platform.h>

#include <dirent.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

static char xr_dir[256];
static char xr_saved_cache_dir[512];
static bool xr_had_cache_dir;

static void xr_remove_dir_tree(const char *dir) {
    DIR *d = opendir(dir);
    if (!d) {
        return;
    }
    struct dirent *ent;
    char path[512];
    while ((ent = readdir(d)) != NULL) {
        if (strcmp(ent->d_name, ".") == 0 || strcmp(ent->d_name, "..") == 0) {
            continue;
        }
        snprintf(path, sizeof(path), "%s/%s", dir, ent->d_name);
        unlink(path);
    }
    closedir(d);
    rmdir(dir);
}

/* Point the cache dir (where cbm_cross_repo_match looks up <project>.db) at a
 * fresh temp directory. Returns the dir path in a static buffer. */
static const char *xr_setup_cache_dir(void) {
    const char *prev = getenv("CBM_CACHE_DIR");
    xr_had_cache_dir = prev != NULL;
    if (prev) {
        snprintf(xr_saved_cache_dir, sizeof(xr_saved_cache_dir), "%s", prev);
    } else {
        xr_saved_cache_dir[0] = '\0';
    }

    snprintf(xr_dir, sizeof(xr_dir), "/tmp/cbm_xrepo_XXXXXX");
    char *made = mkdtemp(xr_dir);
    if (!made) {
        return NULL;
    }
    cbm_setenv("CBM_CACHE_DIR", xr_dir, 1);
    return xr_dir;
}

static void xr_teardown_cache_dir(void) {
    if (xr_dir[0] != '\0') {
        xr_remove_dir_tree(xr_dir);
        xr_dir[0] = '\0';
    }
    if (xr_had_cache_dir) {
        cbm_setenv("CBM_CACHE_DIR", xr_saved_cache_dir, 1);
    } else {
        cbm_unsetenv("CBM_CACHE_DIR");
    }
    xr_had_cache_dir = false;
    xr_saved_cache_dir[0] = '\0';
}

static void xr_db_path(const char *project, char *buf, size_t bufsz) {
    snprintf(buf, bufsz, "%s/%s.db", xr_dir, project);
}

/* Create a source project with a caller Function, a local Route placeholder
 * (the HTTP_CALLS target), and one HTTP_CALLS edge carrying the given props. */
static void xr_make_source(const char *project, const char *call_props) {
    char path[512];
    xr_db_path(project, path, sizeof(path));
    cbm_store_t *s = cbm_store_open_path(path);
    cbm_store_upsert_project(s, project, "/tmp/src");

    cbm_node_t caller = {.project = project,
                         .label = "Function",
                         .name = "callRemote",
                         .qualified_name = "src.callRemote",
                         .file_path = "src/client.ts"};
    int64_t caller_id = cbm_store_upsert_node(s, &caller);

    cbm_node_t local_route = {.project = project,
                              .label = "Route",
                              .name = "remote-call",
                              .qualified_name = "src.remote-call",
                              .file_path = "src/client.ts"};
    int64_t route_id = cbm_store_upsert_node(s, &local_route);

    cbm_edge_t e = {.project = project,
                    .source_id = caller_id,
                    .target_id = route_id,
                    .type = "HTTP_CALLS",
                    .properties_json = call_props};
    cbm_store_insert_edge(s, &e);
    cbm_store_close(s);
}

static void xr_make_async_source(const char *project, const char *call_props) {
    char path[512];
    xr_db_path(project, path, sizeof(path));
    cbm_store_t *s = cbm_store_open_path(path);
    cbm_store_upsert_project(s, project, "/tmp/src");

    cbm_node_t caller = {.project = project,
                         .label = "Function",
                         .name = "publishEvent",
                         .qualified_name = "src.publishEvent",
                         .file_path = "src/publisher.ts"};
    int64_t caller_id = cbm_store_upsert_node(s, &caller);

    cbm_node_t local_route = {.project = project,
                              .label = "Route",
                              .name = "async-call",
                              .qualified_name = "src.async-call",
                              .file_path = "src/publisher.ts"};
    int64_t route_id = cbm_store_upsert_node(s, &local_route);

    cbm_edge_t e = {.project = project,
                    .source_id = caller_id,
                    .target_id = route_id,
                    .type = "ASYNC_CALLS",
                    .properties_json = call_props};
    cbm_store_insert_edge(s, &e);
    cbm_store_close(s);
}

/* Create a target project hosting a Route with the given QN. When
 * with_handler is true, also add a handler Function and a HANDLES edge. */
static void xr_make_target(const char *project, const char *route_qn, bool with_handler) {
    char path[512];
    xr_db_path(project, path, sizeof(path));
    cbm_store_t *s = cbm_store_open_path(path);
    cbm_store_upsert_project(s, project, "/tmp/tgt");

    cbm_node_t route = {.project = project,
                        .label = "Route",
                        .name = "endpoint",
                        .qualified_name = route_qn,
                        .file_path = "tgt/routes.ts"};
    int64_t route_id = cbm_store_upsert_node(s, &route);

    if (with_handler) {
        cbm_node_t handler = {.project = project,
                              .label = "Function",
                              .name = "handleEndpoint",
                              .qualified_name = "tgt.handleEndpoint",
                              .file_path = "tgt/handler.ts"};
        int64_t handler_id = cbm_store_upsert_node(s, &handler);
        cbm_edge_t h = {.project = project,
                        .source_id = handler_id,
                        .target_id = route_id,
                        .type = "HANDLES"};
        cbm_store_insert_edge(s, &h);
    }
    cbm_store_close(s);
}

static int xr_count_cross_type(const char *project, const char *edge_type) {
    char path[512];
    xr_db_path(project, path, sizeof(path));
    cbm_store_t *s = cbm_store_open_path(path);
    cbm_edge_t *edges = NULL;
    int count = 0;
    cbm_store_find_edges_by_type(s, project, edge_type, &edges, &count);
    if (edges) {
        cbm_store_free_edges(edges, count);
    }
    cbm_store_close(s);
    return count;
}

static int xr_count_cross(const char *project) {
    return xr_count_cross_type(project, "CROSS_HTTP_CALLS");
}

static int xr_count_cross_async(const char *project) {
    return xr_count_cross_type(project, "CROSS_ASYNC_CALLS");
}

/* A Route match with NO handler must still produce a forward CROSS_HTTP_CALLS
 * edge. This is the core regression: previously this returned zero. */
TEST(cross_repo_http_match_without_handler) {
    ASSERT_NOT_NULL(xr_setup_cache_dir());
    xr_make_source("xsrc", "{\"url_path\":\"/v1/widgets\"}");
    xr_make_target("xtgt", "__route__ANY__/v1/widgets", false);

    const char *targets[] = {"xtgt"};
    cbm_cross_repo_result_t r = cbm_cross_repo_match("xsrc", targets, 1);

    ASSERT_EQ(r.http_edges, 1);
    ASSERT_EQ(xr_count_cross("xsrc"), 1); /* forward edge written */
    ASSERT_EQ(xr_count_cross("xtgt"), 0); /* no reverse without a handler */
    xr_teardown_cache_dir();
    return 0;
}

/* When the target Route has a handler, the reverse edge is also written. */
TEST(cross_repo_http_reverse_with_handler) {
    ASSERT_NOT_NULL(xr_setup_cache_dir());
    xr_make_source("xsrc", "{\"url_path\":\"/v1/widgets\"}");
    xr_make_target("xtgt", "__route__ANY__/v1/widgets", true);

    const char *targets[] = {"xtgt"};
    cbm_cross_repo_result_t r = cbm_cross_repo_match("xsrc", targets, 1);

    ASSERT_EQ(r.http_edges, 1);
    ASSERT_EQ(xr_count_cross("xsrc"), 1);
    ASSERT_EQ(xr_count_cross("xtgt"), 1); /* reverse edge written */
    xr_teardown_cache_dir();
    return 0;
}

/* The HTTP verb may be stored under "callee" (older extractions) rather than
 * "method"; the method-qualified Route QN must still match via the fallback. */
TEST(cross_repo_method_via_callee) {
    ASSERT_NOT_NULL(xr_setup_cache_dir());
    xr_make_source("xsrc", "{\"url_path\":\"/v1/orders\",\"callee\":\"POST\"}");
    xr_make_target("xtgt", "__route__POST__/v1/orders", false);

    const char *targets[] = {"xtgt"};
    cbm_cross_repo_result_t r = cbm_cross_repo_match("xsrc", targets, 1);

    ASSERT_EQ(r.http_edges, 1);
    ASSERT_EQ(xr_count_cross("xsrc"), 1);
    xr_teardown_cache_dir();
    return 0;
}

/* No Route node in the target → no match (guards against false positives). */
TEST(cross_repo_no_route_no_match) {
    ASSERT_NOT_NULL(xr_setup_cache_dir());
    xr_make_source("xsrc", "{\"url_path\":\"/v1/missing\"}");
    xr_make_target("xtgt", "__route__ANY__/v1/somethingelse", false);

    const char *targets[] = {"xtgt"};
    cbm_cross_repo_result_t r = cbm_cross_repo_match("xsrc", targets, 1);

    ASSERT_EQ(r.http_edges, 0);
    ASSERT_EQ(xr_count_cross("xsrc"), 0);
    xr_teardown_cache_dir();
    return 0;
}

/* ASYNC_CALLS with a target Route and no HANDLES must still match. */
TEST(cross_repo_async_match_without_handler) {
    ASSERT_NOT_NULL(xr_setup_cache_dir());
    xr_make_async_source("xasrc", "{\"url_path\":\"/events/order-created\"}");
    xr_make_target("xatgt", "__route__async__/events/order-created", false);

    const char *targets[] = {"xatgt"};
    cbm_cross_repo_result_t r = cbm_cross_repo_match("xasrc", targets, 1);

    ASSERT_EQ(r.async_edges, 1);
    ASSERT_EQ(xr_count_cross_async("xasrc"), 1);
    ASSERT_EQ(xr_count_cross_async("xatgt"), 0);
    xr_teardown_cache_dir();
    return 0;
}

void suite_cross_repo(void) {
    RUN_TEST(cross_repo_http_match_without_handler);
    RUN_TEST(cross_repo_http_reverse_with_handler);
    RUN_TEST(cross_repo_method_via_callee);
    RUN_TEST(cross_repo_no_route_no_match);
    RUN_TEST(cross_repo_async_match_without_handler);
}

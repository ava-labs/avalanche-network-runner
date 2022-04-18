use std::env::args;

use log::info;
use tokio::runtime::Runtime;

use avalanche_network_runner_sdk::Client;

/// cargo run --example stop -- [HTTP RPC ENDPOINT]
/// cargo run --example stop -- http://127.0.0.1:8080
fn main() {
    // ref. https://github.com/env-logger-rs/env_logger/issues/47
    env_logger::init_from_env(
        env_logger::Env::default().filter_or(env_logger::DEFAULT_FILTER_ENV, "info"),
    );

    let url = args().nth(1).expect("no url given");
    let rt = Runtime::new().unwrap();

    info!("creating client");
    let cli = rt.block_on(Client::new(&url));

    let resp = rt.block_on(cli.stop()).expect("failed stop");
    info!("stop response: {:?}", resp);
}
